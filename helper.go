package gkube

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/transport/spdy"
	k8sExec "k8s.io/utils/exec"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type (
	GracePeriodSeconds     = client.GracePeriodSeconds
	Preconditions          = client.Preconditions
	PropagationPolicy      = client.PropagationPolicy
	MatchingLabels         = client.MatchingLabels
	HasLabels              = client.HasLabels
	MatchingLabelsSelector = client.MatchingLabelsSelector
	MatchingFields         = client.MatchingFields
	MatchingFieldsSelector = client.MatchingFieldsSelector
	InNamespace            = client.InNamespace
	Limit                  = client.Limit
	Continue               = client.Continue
)

type KubernetesHelper interface {
	Create(context.Context, client.Object, ...client.CreateOption) error
	Delete(context.Context, client.Object, ...client.DeleteOption) error
	DeleteAllOf(context.Context, client.Object, ...client.DeleteAllOfOption) error
	Get(context.Context, client.Object) error
	List(context.Context, client.ObjectList, ...client.ListOption) error

	Object(Gomega, context.Context, client.Object) client.Object
	Objects(Gomega, context.Context, client.ObjectList, ...client.ListOption) client.ObjectList
	Patch(Gomega, context.Context, client.Object, client.Patch, ...client.PatchOption) error
	Update(Gomega, context.Context, client.Object, controllerutil.MutateFn, ...client.UpdateOption) error
	UpdateStatus(Gomega, context.Context, client.Object, controllerutil.MutateFn, ...client.SubResourceUpdateOption) error

	// Pod Extension
	Evict(context.Context, *corev1.Pod, ...client.DeleteOption) error
	Exec(context.Context, *corev1.Pod, string, []string, io.Writer, io.Writer) (*PodSession, error)
	Logs(context.Context, *corev1.Pod, *corev1.PodLogOptions, io.Writer, io.Writer) (*PodSession, error)
	PortForward(context.Context, *corev1.Pod, []string, io.Writer, io.Writer) (*portforward.PortForwarder, error)

	ProxyGet(context.Context, client.Object, string, string, string, map[string]string, io.Writer, io.Writer) (*PodSession, error)
}

// helper contains
type helper struct {
	Scheme           *runtime.Scheme
	Config           *rest.Config
	Client           client.Client
	Clientset        *kubernetes.Clientset
	PodRestInterface rest.Interface
}

func NewKubernetesHelper(opts ...HelperOption) KubernetesHelper {
	return newKubernetesHelper(opts...)
}

func newKubernetesHelper(opts ...HelperOption) *helper {
	helper := &helper{}
	helper.ApplyOptions(opts)

	if helper.Config == nil {
		conf, err := config.GetConfig()
		Expect(err).ShouldNot(HaveOccurred())
		helper.Config = conf
	}

	if helper.Scheme == nil {
		helper.Scheme = scheme.Scheme
	}

	if helper.Client == nil {
		c, err := client.New(helper.Config, client.Options{
			Scheme: helper.Scheme,
		})
		Expect(err).ShouldNot(HaveOccurred())
		helper.Client = c
	}

	podRestInterface, err := apiutil.RESTClientForGVK(schema.GroupVersionKind{
		Version: "v1",
		Kind:    "Pod",
	}, false, helper.Config, serializer.NewCodecFactory(helper.Scheme))
	Expect(err).ShouldNot(HaveOccurred())
	helper.PodRestInterface = podRestInterface

	clientset, err := kubernetes.NewForConfig(helper.Config)
	Expect(err).ShouldNot(HaveOccurred())
	helper.Clientset = clientset

	return helper
}

func (h *helper) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	return handleError(h.Client.Create(ctx, obj, opts...))
}

func (h *helper) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	return handleError(client.IgnoreNotFound(h.Client.Delete(ctx, obj, opts...)))
}

func (h *helper) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	return handleError(h.Client.DeleteAllOf(ctx, obj, opts...))
}

func (h *helper) Evict(ctx context.Context, pod *corev1.Pod, opts ...client.DeleteOption) error {
	deleteOptions := &client.DeleteOptions{}
	for _, deleteOption := range opts {
		deleteOption.ApplyToDelete(deleteOptions)
	}

	return handleError(h.Client.SubResource("eviction").Create(ctx, pod, &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{
			Name: pod.GetName(),
		},
		DeleteOptions: deleteOptions.AsDeleteOptions(),
	}))
}

func (h *helper) Exec(ctx context.Context, pod *corev1.Pod, container string, command []string, outWriter, errWriter io.Writer) (*PodSession, error) {
	session := &PodSession{
		Out:      gbytes.NewBuffer(),
		Err:      gbytes.NewBuffer(),
		lock:     &sync.Mutex{},
		exitCode: -1,
	}

	var commandOut, commandErr io.Writer

	commandOut, commandErr = session.Out, session.Err

	if outWriter != nil {
		commandOut = io.MultiWriter(commandOut, outWriter)
	}

	if errWriter != nil {
		commandErr = io.MultiWriter(commandErr, errWriter)
	}

	streamOpts := remotecommand.StreamOptions{
		Stdout: commandOut,
		Stderr: commandErr,
	}

	podExecOpts := &corev1.PodExecOptions{
		Stdout:    true,
		Stderr:    true,
		Stdin:     false,
		TTY:       false,
		Container: container,
		Command:   command,
	}

	execReq := h.PodRestInterface.Post().
		Resource("pods").
		Name(pod.Name).
		Namespace(pod.Namespace).
		SubResource("exec").
		//Timeout(timeout).
		VersionedParams(podExecOpts, scheme.ParameterCodec)

	// if context has a deadline add a timeout
	// this can be removed after PR below merges
	// NOTE not comptaible with https://pkg.go.dev/github.com/onsi/ginkgo/v2#SpecContext
	if deadline, ok := ctx.Deadline(); ok {
		execReq = execReq.Timeout(deadline.Sub(time.Now()))
	}

	executor, err := remotecommand.NewSPDYExecutor(h.Config, http.MethodPost, execReq.URL())
	if err != nil {
		return nil, err
	}

	go func() {
		// https://github.com/kubernetes/kubernetes/pull/103177
		// update to make cancellable with ctx after this merges
		err := executor.Stream(streamOpts)
		session.Out.Close()
		session.Err.Close()
		if err != nil {
			fmt.Fprintf(errWriter, err.Error())
			if exitcode, ok := err.(k8sExec.CodeExitError); ok {
				session.exitCode = exitcode.Code
				return
			}
			session.exitCode = 254
			return
		}
		session.exitCode = 0
	}()
	return session, nil
}

// Get gets the object from the API server.
func (h *helper) Get(ctx context.Context, obj client.Object) error {
	return handleError(h.Client.Get(ctx, client.ObjectKeyFromObject(obj), obj))
}

// Log streams logs from a container
func (h *helper) Logs(ctx context.Context, pod *corev1.Pod, podLogOptions *corev1.PodLogOptions, outWriter, errWriter io.Writer) (*PodSession, error) {
	stream, err := h.Clientset.CoreV1().
		Pods(pod.Namespace).
		GetLogs(pod.Name, podLogOptions).
		Stream(ctx)
	if err != nil {
		return nil, err
	}

	session := &PodSession{
		Out:      gbytes.NewBuffer(),
		lock:     &sync.Mutex{},
		exitCode: -1,
	}

	var logOut io.Writer = session.Out

	if outWriter != nil {
		logOut = io.MultiWriter(logOut, outWriter)
	}

	go func() {
		defer stream.Close()
		_, err := io.Copy(logOut, stream)
		if err != nil {
			fmt.Fprintf(errWriter, err.Error())
			session.exitCode = 254
			return
		}
		session.exitCode = 0
	}()

	return session, nil
}

// List gets the list object from the API server.
func (h *helper) List(ctx context.Context, obj client.ObjectList, listOptions ...client.ListOption) error {
	return handleError(h.Client.List(ctx, obj, listOptions...))
}

// Object gets and returns the object itself
func (h *helper) Object(g Gomega, ctx context.Context, obj client.Object) client.Object {
	g.Expect(handleError(h.Client.Get(ctx, client.ObjectKeyFromObject(obj), obj))).Should(Succeed())
	return obj
}

// Objects gets a list of objects
func (h *helper) Objects(g Gomega, ctx context.Context, obj client.ObjectList, listOptions ...client.ListOption) client.ObjectList {
	g.Expect(handleError(h.Client.List(ctx, obj, listOptions...))).Should(Succeed())
	return obj
}

// Patch patches an object
func (h *helper) Patch(g Gomega, ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	g.Expect(handleError(h.Client.Get(ctx, client.ObjectKeyFromObject(obj), obj))).Should(Succeed())
	return handleError(h.Client.Patch(ctx, obj, patch, opts...))
}

// PortForward opens a pesistent portforwarding session, must be closed by user
func (h *helper) PortForward(ctx context.Context, pod *corev1.Pod, ports []string, outWriter, errWriter io.Writer) (*portforward.PortForwarder, error) {
	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", pod.GetNamespace(), pod.GetName())
	hostIP := strings.TrimLeft(h.Config.Host, "htps:/")
	serverURL := url.URL{Scheme: "https", Path: path, Host: hostIP}

	roundTripper, upgrader, err := spdy.RoundTripperFor(h.Config)
	if err != nil {
		return nil, err
	}
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: roundTripper}, http.MethodPost, &serverURL)

	stopCh, readyCh := make(chan struct{}, 1), make(chan struct{}, 1)

	pf, err := portforward.New(dialer, ports, stopCh, readyCh, outWriter, errWriter)
	if err != nil {
		return nil, err
	}

	// Attempt to start port forwarding asynchronously
	go func() {
		err = pf.ForwardPorts()
		if err != nil {
			close(stopCh)
		}
	}()

	// Return when port forwarding is either ready or an error occurs
	select {
	case <-readyCh:
		return pf, nil
	case <-stopCh:
		return nil, err
	case <-ctx.Done():
		pf.Close()
		return nil, nil
	}
}

// ProxyGet will perform a HTTP GET on the specified pod or service via
// Kubernetes proxy
func (h *helper) ProxyGet(ctx context.Context, obj client.Object, scheme, port, path string, params map[string]string, outWriter, errWriter io.Writer) (*PodSession, error) {
	var stream io.ReadCloser
	var err error

	switch obj.(type) {
	case *corev1.Pod:
		stream, err = h.Clientset.CoreV1().
			Pods(obj.GetNamespace()).ProxyGet(scheme, obj.GetName(), port, path, params).
			Stream(ctx)
	case *corev1.Service:
		stream, err = h.Clientset.CoreV1().
			Services(obj.GetNamespace()).ProxyGet(scheme, obj.GetName(), port, path, params).
			Stream(ctx)
	default:
		return nil, fmt.Errorf("expected a Pod or Service, got %T", obj)
	}
	if err != nil {
		return nil, err
	}

	session := &PodSession{
		Out:      gbytes.NewBuffer(),
		lock:     &sync.Mutex{},
		exitCode: -1,
	}

	var logOut io.Writer = session.Out

	if outWriter != nil {
		logOut = io.MultiWriter(logOut, outWriter)
	}

	go func() {
		defer stream.Close()
		_, err := io.Copy(logOut, stream)
		if err != nil {
			fmt.Fprintf(errWriter, err.Error())
			session.exitCode = 254
			return
		}
		session.exitCode = 0
	}()

	return session, nil
}

func (h *helper) Update(g Gomega, ctx context.Context, obj client.Object, f controllerutil.MutateFn, opts ...client.UpdateOption) error {
	g.Expect(handleError(h.Client.Get(ctx, client.ObjectKeyFromObject(obj), obj))).Should(Succeed())
	g.Expect(mutate(f, client.ObjectKeyFromObject(obj), obj)).Should(Succeed())
	return handleError(h.Client.Update(ctx, obj, opts...))
}

func (h *helper) UpdateStatus(g Gomega, ctx context.Context, obj client.Object, f controllerutil.MutateFn, opts ...client.SubResourceUpdateOption) error {
	g.Expect(handleError(h.Client.Get(ctx, client.ObjectKeyFromObject(obj), obj))).Should(Succeed())
	g.Expect(mutate(f, client.ObjectKeyFromObject(obj), obj)).Should(Succeed())
	return handleError(h.Client.Status().Update(ctx, obj, opts...))
}

// mutate wraps a MutateFn and applies validation to its result.
func mutate(f controllerutil.MutateFn, key client.ObjectKey, obj client.Object) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s", r)
		}
	}()
	if err := f(); err != nil {
		return err
	}
	if newKey := client.ObjectKeyFromObject(obj); key != newKey {
		return fmt.Errorf("MutateFn cannot mutate object name and/or object namespace")
	}
	return nil
}

func handleError(err error) error {
	if err == nil {
		return nil
	}

	// Client issue
	if isRuntime(err) {
		StopTrying("Stopped Trying").Wrap(err).Now()
	}

	// Generic connection issue
	// return generic errors so they can be retried (maybe handle other http issues before returning)
	statusErr, ok := err.(*k8sErrors.StatusError)
	if !ok {
		return err
	}

	if secondsToDelay, ok := k8sErrors.SuggestsClientDelay(statusErr); ok {
		TryAgainAfter(time.Duration(secondsToDelay) * time.Second).Wrap(statusErr).Now()
	}

	// if it can be retried
	if IsRetryable(statusErr) {
		return statusErr
	}

	StopTrying("Stopped Trying").Wrap(statusErr).Now()

	return err
}

func isRuntime(err error) bool {
	return runtime.IsMissingKind(err) ||
		runtime.IsMissingVersion(err) ||
		runtime.IsNotRegisteredError(err) ||
		runtime.IsStrictDecodingError(err)
}

func IsRetryable(err error) bool {
	reason := k8sErrors.ReasonForError(err)
	_, isRetryable := retryableReasons[reason]
	return isRetryable
}

var retryableReasons = map[metav1.StatusReason]struct{}{
	metav1.StatusReasonNotFound:           {},
	metav1.StatusReasonServerTimeout:      {},
	metav1.StatusReasonTimeout:            {},
	metav1.StatusReasonTooManyRequests:    {},
	metav1.StatusReasonInternalError:      {},
	metav1.StatusReasonServiceUnavailable: {},
}

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
	Create(context.Context, client.Object, ...client.CreateOption) func() error
	Delete(context.Context, client.Object, ...client.DeleteOption) func() error
	DeleteAllOf(context.Context, client.Object, ...client.DeleteAllOfOption) func() error
	Get(context.Context, client.Object) func() error
	List(context.Context, client.ObjectList, ...client.ListOption) func() error
	Object(context.Context, client.Object) func(Gomega) client.Object
	Objects(context.Context, client.ObjectList, ...client.ListOption) func(Gomega) client.ObjectList
	Patch(context.Context, client.Object, client.Patch, ...client.PatchOption) func(Gomega) error
	Update(context.Context, client.Object, controllerutil.MutateFn, ...client.UpdateOption) func(Gomega) error
	UpdateStatus(context.Context, client.Object, controllerutil.MutateFn, ...client.UpdateOption) func(Gomega) error

	Exec(context.Context, *corev1.Pod, string, []string, io.Writer, io.Writer) (*PodSession, error)
	Logs(context.Context, *corev1.Pod, *corev1.PodLogOptions, io.Writer, io.Writer) (*PodSession, error)
	PortForward(context.Context, client.Object, []string, io.Writer, io.Writer) (*portforward.PortForwarder, error)
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

func (h *helper) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) func() error {
	return func() error {
		return h.Client.Create(ctx, obj, opts...)
	}
}

func (h *helper) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) func() error {
	return func() error {
		return client.IgnoreNotFound(h.Client.Delete(ctx, obj, opts...))
	}
}

func (h *helper) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) func() error {
	return func() error {
		return h.Client.DeleteAllOf(ctx, obj, opts...)
	}
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
func (h *helper) Get(ctx context.Context, obj client.Object) func() error {
	return func() error {
		return h.Client.Get(ctx, client.ObjectKeyFromObject(obj), obj)
	}
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
func (h *helper) List(ctx context.Context, obj client.ObjectList, listOptions ...client.ListOption) func() error {
	return func() error {
		return h.Client.List(ctx, obj, listOptions...)
	}
}

// Object gets and returns the object itself
func (h *helper) Object(ctx context.Context, obj client.Object) func(Gomega) client.Object {
	return func(g Gomega) client.Object {
		g.Expect(h.Client.Get(ctx, client.ObjectKeyFromObject(obj), obj)).Should(Succeed())
		return obj
	}
}

// Objects gets a list of objects
func (h *helper) Objects(ctx context.Context, obj client.ObjectList, listOptions ...client.ListOption) func(Gomega) client.ObjectList {
	return func(g Gomega) client.ObjectList {
		g.Expect(h.Client.List(ctx, obj, listOptions...)).Should(Succeed())
		return obj
	}
}

// Patch patches an object
func (h *helper) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) func(Gomega) error {
	return func(g Gomega) error {
		g.Expect(h.Client.Get(ctx, client.ObjectKeyFromObject(obj), obj)).Should(Succeed())
		return h.Client.Patch(ctx, obj, patch, opts...)
	}
}

// PortForward opens a pesistent portforwarding session, must be closed by user
func (h *helper) PortForward(ctx context.Context, obj client.Object, ports []string, outWriter, errWriter io.Writer) (*portforward.PortForwarder, error) {
	var path string
	switch obj.(type) {
	case *corev1.Pod:
		path = fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", obj.GetNamespace(), obj.GetName())
	case *corev1.Service:
		path = fmt.Sprintf("/api/v1/namespaces/%s/services/%s/portforward", obj.GetNamespace(), obj.GetName())
	default:
		return nil, fmt.Errorf("expected a Pod or Service, got %T", obj)
	}

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

func (h *helper) Update(ctx context.Context, obj client.Object, f controllerutil.MutateFn, opts ...client.UpdateOption) func(Gomega) error {
	return func(g Gomega) error {
		g.Expect(h.Client.Get(ctx, client.ObjectKeyFromObject(obj), obj)).Should(Succeed())
		g.Expect(mutate(f, client.ObjectKeyFromObject(obj), obj)).Should(Succeed())
		return h.Client.Update(ctx, obj, opts...)
	}
}

func (h *helper) UpdateStatus(ctx context.Context, obj client.Object, f controllerutil.MutateFn, opts ...client.UpdateOption) func(Gomega) error {
	return func(g Gomega) error {
		g.Expect(h.Client.Get(ctx, client.ObjectKeyFromObject(obj), obj)).Should(Succeed())
		g.Expect(mutate(f, client.ObjectKeyFromObject(obj), obj)).Should(Succeed())
		return h.Client.Status().Update(ctx, obj, opts...)
	}
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

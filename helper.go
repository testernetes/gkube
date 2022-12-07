package gkube

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
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
	Create(client.Object, ...client.CreateOption) func() error
	Delete(client.Object, ...client.DeleteOption) func() error
	DeleteAllOf(client.Object, ...client.DeleteAllOfOption) func() error
	Get(client.Object) func() error
	List(client.ObjectList, ...client.ListOption) func() error
	Object(client.Object) func(g Gomega) client.Object
	Objects(client.ObjectList, ...client.ListOption) func(g Gomega) client.ObjectList
	Patch(client.Object, client.Patch, ...client.PatchOption) func(g Gomega) error
	Update(client.Object, controllerutil.MutateFn, ...client.UpdateOption) func(g Gomega) error
	UpdateStatus(client.Object, controllerutil.MutateFn, ...client.UpdateOption) func(g Gomega) error

	Exec(*corev1.Pod, *corev1.PodExecOptions, time.Duration, io.Writer, io.Writer) (*PodSession, error)
	Logs(*corev1.Pod, *corev1.PodLogOptions, io.Writer) (*PodSession, error)
	PortForward(client.Object, []string, io.Writer, io.Writer) (*portforward.PortForwarder, error)
	ProxyGet(client.Object, string, string, string, map[string]string, io.Writer) (*PodSession, error)
}

// helper contains
type helper struct {
	Scheme           *runtime.Scheme
	Config           *rest.Config
	Client           client.Client
	Clientset        *kubernetes.Clientset
	Context          context.Context
	PodRestInterface rest.Interface

	Signals chan os.Signal
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

	if helper.Context == nil {
		helper.Context = context.TODO()
	}

	podRestInterface, err := apiutil.RESTClientForGVK(schema.GroupVersionKind{
		Version: "v1",
		Kind:    "Pod",
	}, false, helper.Config, serializer.NewCodecFactory(helper.Scheme))
	Expect(err).ShouldNot(HaveOccurred())
	helper.PodRestInterface = podRestInterface

	clientset, err := kubernetes.NewForConfig(helper.Config)
	if err != nil {
		panic("error in getting access to K8S")
	}
	helper.Clientset = clientset

	helper.Signals = make(chan os.Signal, 1)
	signal.Notify(helper.Signals, os.Interrupt)

	return helper
}

func (h *helper) Create(obj client.Object, opts ...client.CreateOption) func() error {
	return func() error {
		select {
		case <-h.Signals:
			panic("Interrupted by User")
		default:
			return h.Client.Create(h.Context, obj, opts...)
		}
	}
}

func (h *helper) Delete(obj client.Object, opts ...client.DeleteOption) func() error {
	return func() error {
		select {
		case <-h.Signals:
			panic("Interrupted by User")
		default:
			return client.IgnoreNotFound(h.Client.Delete(h.Context, obj, opts...))
		}
	}
}

func (h *helper) DeleteAllOf(obj client.Object, opts ...client.DeleteAllOfOption) func() error {
	return func() error {
		select {
		case <-h.Signals:
			panic("Interrupted by User")
		default:
			return h.Client.DeleteAllOf(h.Context, obj, opts...)
		}
	}
}

func (h *helper) Exec(pod *corev1.Pod, podExecOpts *corev1.PodExecOptions, timeout time.Duration, outWriter, errWriter io.Writer) (*PodSession, error) {
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

	execReq := h.PodRestInterface.Post().
		Resource("pods").
		Name(pod.Name).
		Namespace(pod.Namespace).
		SubResource("exec").
		Timeout(timeout).
		VersionedParams(podExecOpts, scheme.ParameterCodec)

	// https://github.com/kubernetes/kubernetes/pull/103177
	// update to cancel commands which timeout after this merges
	executor, err := remotecommand.NewSPDYExecutor(h.Config, http.MethodPost, execReq.URL())
	if err != nil {
		return session, err
	}

	go func() {
		err := executor.Stream(streamOpts)
		session.Out.Close()
		session.Err.Close()
		if err != nil {
			if exitcode, ok := err.(k8sExec.CodeExitError); ok {
				session.exitCode = exitcode.Code
				return
			}
			session.exitCode = 254
			return
		}
		session.exitCode = 0
	}()
	return session, err
}

// Get gets the object from the API server.
func (h *helper) Get(obj client.Object) func() error {
	return func() error {
		select {
		case <-h.Signals:
			panic("Interrupted by User")
		default:
			return h.Client.Get(h.Context, client.ObjectKeyFromObject(obj), obj)
		}
	}
}

// List gets the list object from the API server.
func (h *helper) Logs(pod *corev1.Pod, podLogOptions *corev1.PodLogOptions, outWriter io.Writer) (*PodSession, error) {
	stream, err := h.Clientset.CoreV1().
		Pods(pod.Namespace).
		GetLogs(pod.Name, podLogOptions).
		Stream(context.TODO())
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
			session.exitCode = 254
			return
		}
		session.exitCode = 0
	}()

	return session, nil
}

// List gets the list object from the API server.
func (h *helper) List(obj client.ObjectList, listOptions ...client.ListOption) func() error {
	return func() error {
		select {
		case <-h.Signals:
			panic("Interrupted by User")
		default:
			return h.Client.List(h.Context, obj, listOptions...)
		}
	}
}

func (h *helper) Object(obj client.Object) func(g Gomega) client.Object {
	return func(g Gomega) client.Object {
		select {
		case <-h.Signals:
			panic("Interrupted by User")
		default:
			g.Expect(h.Client.Get(h.Context, client.ObjectKeyFromObject(obj), obj)).Should(Succeed())
			return obj
		}
	}
}

func (h *helper) Objects(obj client.ObjectList, listOptions ...client.ListOption) func(g Gomega) client.ObjectList {
	return func(g Gomega) client.ObjectList {
		select {
		case <-h.Signals:
			panic("Interrupted by User")
		default:
			g.Expect(h.Client.List(h.Context, obj, listOptions...)).Should(Succeed())
			return obj
		}
	}
}

func (h *helper) Patch(obj client.Object, patch client.Patch, opts ...client.PatchOption) func(g Gomega) error {
	key := client.ObjectKeyFromObject(obj)
	return func(g Gomega) error {
		select {
		case <-h.Signals:
			panic("Interrupted by User")
		default:
			g.Expect(h.Client.Get(h.Context, key, obj)).Should(Succeed())
			return h.Client.Patch(h.Context, obj, patch, opts...)
		}
	}
}

func (h *helper) PortForward(obj client.Object, ports []string, outWriter, errWriter io.Writer) (*portforward.PortForwarder, error) {
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
	case <-h.Signals:
		panic("Interrupted by User")
	}
}

// ProxyGet will perform a HTTP GET on the specified pod or service via
// Kubernetes proxy
func (h *helper) ProxyGet(obj client.Object, scheme, port, path string, params map[string]string, outWriter io.Writer) (*PodSession, error) {
	var stream io.ReadCloser
	var err error

	switch obj.(type) {
	case *corev1.Pod:
		stream, err = h.Clientset.CoreV1().
			Pods(obj.GetNamespace()).ProxyGet(scheme, obj.GetName(), port, path, params).
			Stream(context.TODO())
	case *corev1.Service:
		stream, err = h.Clientset.CoreV1().
			Services(obj.GetNamespace()).ProxyGet(scheme, obj.GetName(), port, path, params).
			Stream(context.TODO())
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
			session.exitCode = 254
			return
		}
		session.exitCode = 0
	}()

	return session, nil
}

func (h *helper) Update(obj client.Object, f controllerutil.MutateFn, opts ...client.UpdateOption) func(g Gomega) error {
	key := client.ObjectKeyFromObject(obj)
	return func(g Gomega) error {
		select {
		case <-h.Signals:
			panic("Interrupted by User")
		default:
			g.Expect(h.Client.Get(h.Context, key, obj)).Should(Succeed())
			g.Expect(mutate(f, key, obj)).Should(Succeed())
			return h.Client.Update(h.Context, obj, opts...)
		}
	}
}

func (h *helper) UpdateStatus(obj client.Object, f controllerutil.MutateFn, opts ...client.UpdateOption) func(g Gomega) error {
	key := client.ObjectKeyFromObject(obj)
	return func(g Gomega) error {
		select {
		case <-h.Signals:
			panic("Interrupted by User")
		default:
			g.Expect(h.Client.Get(h.Context, key, obj)).Should(Succeed())
			g.Expect(mutate(f, key, obj)).Should(Succeed())
			return h.Client.Status().Update(h.Context, obj, opts...)
		}
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

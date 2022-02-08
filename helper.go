package gkube

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
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

	Exec(*corev1.Pod, *corev1.PodExecOptions, time.Duration, io.Writer, io.Writer) (*PodSession, error)
	Object(client.Object) func(g Gomega) client.Object
	Objects(client.ObjectList, ...client.ListOption) func(g Gomega) client.ObjectList
	Patch(client.Object, client.Patch, ...client.PatchOption) func(g Gomega) error
	Update(client.Object, controllerutil.MutateFn, ...client.UpdateOption) func(g Gomega) error
	UpdateStatus(client.Object, controllerutil.MutateFn, ...client.UpdateOption) func(g Gomega) error
}

// helper contains
type helper struct {
	Scheme           *runtime.Scheme
	Config           *rest.Config
	Client           client.Client
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

	//clientset, err := kubernetes.NewForConfig(helper.Config)
	//if err != nil {
	//	panic("error in getting access to K8S")
	//}
	//clientset.CoreV1().RESTClient()

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

// Get gets the object froh the API server.
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

// List gets the list object froh the API server.
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

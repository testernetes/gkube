package gkube

import (
	"context"
	"fmt"
	"io"
	"net/http"
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

	Exec(*corev1.Pod, *corev1.PodExecOptions, io.Writer, io.Writer) (*PodSession, error)
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
}

func NewKubernetesHelper(opts ...HelperOption) KubernetesHelper {
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

	return helper
}

func (m *helper) Create(obj client.Object, opts ...client.CreateOption) func() error {
	return func() error {
		return m.Client.Create(m.Context, obj, opts...)
	}
}

func (m *helper) Delete(obj client.Object, opts ...client.DeleteOption) func() error {
	return func() error {
		return m.Client.Delete(m.Context, obj, opts...)
	}
}

func (m *helper) DeleteAllOf(obj client.Object, opts ...client.DeleteAllOfOption) func() error {
	return func() error {
		return m.Client.DeleteAllOf(m.Context, obj, opts...)
	}
}

func (h *helper) Exec(pod *corev1.Pod, podExecOpts *corev1.PodExecOptions, outWriter io.Writer, errWriter io.Writer) (*PodSession, error) {
	exited := make(chan struct{})

	session := &PodSession{
		Out:      gbytes.NewBuffer(),
		Err:      gbytes.NewBuffer(),
		Exited:   exited,
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
		Timeout(45*time.Second).
		VersionedParams(podExecOpts, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(h.Config, http.MethodPost, execReq.URL())
	if err != nil {
		return session, err
	}

	go func() {
		session.lock.Lock()
		err := executor.Stream(streamOpts)
		defer session.lock.Unlock()
		defer close(exited)
		if err != nil {
			if exitcode, ok := err.(k8sExec.CodeExitError); ok {
				session.Code = exitcode.Code
			}
			return
		}
		session.Code = 0
	}()
	return session, err
}

// Get gets the object from the API server.
func (m *helper) Get(obj client.Object) func() error {
	return func() error {
		return m.Client.Get(m.Context, client.ObjectKeyFromObject(obj), obj)
	}
}

// List gets the list object from the API server.
func (m *helper) List(obj client.ObjectList, listOptions ...client.ListOption) func() error {
	return func() error {
		return m.Client.List(m.Context, obj, listOptions...)
	}
}

func (m *helper) Object(obj client.Object) func(g Gomega) client.Object {
	return func(g Gomega) client.Object {
		g.Expect(m.Client.Get(m.Context, client.ObjectKeyFromObject(obj), obj)).Should(Succeed())
		return obj
	}
}

func (m *helper) Objects(obj client.ObjectList, listOptions ...client.ListOption) func(g Gomega) client.ObjectList {
	return func(g Gomega) client.ObjectList {
		g.Expect(m.Client.List(m.Context, obj, listOptions...)).Should(Succeed())
		return obj
	}
}

func (m *helper) Patch(obj client.Object, patch client.Patch, opts ...client.PatchOption) func(g Gomega) error {
	key := client.ObjectKeyFromObject(obj)
	return func(g Gomega) error {
		g.Expect(m.Client.Get(m.Context, key, obj)).Should(Succeed())
		return m.Client.Patch(m.Context, obj, patch, opts...)
	}
}

func (m *helper) Update(obj client.Object, f controllerutil.MutateFn, opts ...client.UpdateOption) func(g Gomega) error {
	key := client.ObjectKeyFromObject(obj)
	return func(g Gomega) error {
		g.Expect(m.Client.Get(m.Context, key, obj)).Should(Succeed())
		g.Expect(mutate(f, key, obj)).Should(Succeed())
		return m.Client.Update(m.Context, obj, opts...)
	}
}

func (m *helper) UpdateStatus(obj client.Object, f controllerutil.MutateFn, opts ...client.UpdateOption) func(g Gomega) error {
	key := client.ObjectKeyFromObject(obj)
	return func(g Gomega) error {
		g.Expect(m.Client.Get(m.Context, key, obj)).Should(Succeed())
		g.Expect(mutate(f, key, obj)).Should(Succeed())
		return m.Client.Status().Update(m.Context, obj, opts...)
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

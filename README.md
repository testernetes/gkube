# gKube

A Gomega Kubernetes API matcher/assertion library to help make writing tests simpler with less boilerplate logic.
It wraps the controller-runtime dynamic client and includes some pod sub-resource helpers using client-go.

A simple example of creating a namespace and asserting on some fields with JSONPath:
```go
package simple

import (
	"testing"

	. "github.com/matt-simons/gkube"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Simple use of the KubernetesHelper", Ordered, func() {

	var k8s KubernetesHelper
	var namespace *corev1.Namespace

	BeforeAll(func() {
		k8s = NewKubernetesHelper()
		namespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "simple",
			},
		}
	})

	It("should create a namespace", func() {
		Eventually(k8s.Create(namespace)).Should(Succeed())
		Eventually(k8s.Object(namespace)).Should(
			WithJSONPath("{.status.phase}", Equal(corev1.NamespacePhase("Active"))),
		)
	})

	It("should filter a list of namespaces using a JSONPath", func() {
		Eventually(k8s.Objects(&corev1.NamespaceList{})).Should(
			WithJSONPath("{.items[*].metadata.name}", ContainElement("simple")),
		)
	})

	AfterAll(func() {
		Eventually(k8s.Delete(namespace, GracePeriodSeconds(30))).Should(Succeed())
	})
})

func TestKubernetesHelper(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Simple")
}
```

More examples can be found at [https://github.com/matt-simons/gkube-examples](https://github.com/matt-simons/gkube-examples)

## Helper

A Kubernetes Helper interface designed to be used with Gomega assertions.

### Options

The helper can be constucted by providing zero or many options:

* `WithClient` passes a client for interacting with Kubernetes API servers.
* `WithConfig` passes config for initializing REST configs for talking to the Kubernetes API.
* `WithScheme` passes a scheme which contains information associating Go types with Kubernetes groups, versions, and kinds.
* `WithContext` passes a context which carries deadlines, cancellation signals, and other request-scoped values.

Example for using the KubernetesHelper with Openshift Custom Resources:
```go
import openshiftapi "github.com/openshift/api"

...

scheme := runtime.NewScheme()
openshiftapi.Install(scheme)
k8s := NewKubernetesHelper(WithScheme(scheme))

```

### Client Helpers

Create, Delete, DeleteAllOf, Get, List, Patch, Update wrap their respective client functions by returning a function which can be called by Gomega's Eventually or Consistently. The function returns an error if it occurs.

### Object Helpers

Object and Objects use Get to retrieve a specificed Object or ObjectList, however they are wrapped in a `func() client.Object`
which is passed to a matcher.

When using Object the object's name must be provided and namespace if it is namespaced.

### Client Options

The controller-runtime's [client](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/client) options are also surfaced for convience when gkube is dot imported.
Zero or many options can be provided to all compatible helpers, e.g.:
```go
Eventually(k8s.Objects(&corev1.ConfigMapList{}, InNamespace("default"))).Should(WithJSONPath(
	"{.items[*].metadata.name}", ContainElement("my-configmap")))

Eventually(k8s.DeleteAllOf(&corev1.Pod{},
	InNamespace("default"), MatchingLabels{"app": "foo"})).Should(Succeed())
```

### Pod Subresource Helpers

These helpers interact with some of the more useful pod subresources.

#### Exec

Executes a command in a running pod, this helper functions similarly to [gexec](https://onsi.github.io/gomega/#gexec-testing-external-processes).
It allows you to assert against the exit code, and stream output into gbytes.Buffers to allow you make assertions against output.

#### Log

TBD

#### PortForward

TBD

## Matchers

### WithJSONPath

`WithJSONPath` is a [transformer](https://onsi.github.io/gomega/#withtransformtransform-interface-matcher-gomegamatcher) function. It transforms the expected object by an expression then evaluates a matcher against the result(s), it can be used in conjunction with the `Object` or `Objects` helpers.

If only one result is produced from the expression then the results list is flattened for convience. It's is generally recommended to keep expressions simple and specific to a field. If you need to validate multiple fields, use multiple assertions or the [EqualObject](#EqualObject) matcher.

See Kubernetes JSONPath documentation for more details and examples: https://kubernetes.io/docs/reference/kubectl/jsonpath/

Example asserting a pod phase:
```go
Eventually(k8s.Object(pod)).WithTimeout(time.Minute).Should(WithJSONPath(
	"{.status.phase}", BeEquivalentTo(corev1.PodRunning),
)
```

Example asserting a specific condition from a deployment:
```go
Eventually(k8s.Object(deployment)).WithTimeout(time.Minute).Should(WithJSONPath(
	`{.status.conditions[?(@.type=="Available")]}`,
	MatchFields(IgnoreExtras, Fields{
		"Status": BeEquivalentTo(corev1.ConditionTrue),
		"Reason": Equal("MinimumReplicasAvailable"),
	}),
))
```

### EqualObject

From the CAPI project: [https://github.com/kubernetes-sigs/cluster-api/blob/main/internal/matchers/matchers.go](https://github.com/kubernetes-sigs/cluster-api/blob/main/internal/matchers/matchers.go).

Can be used in conjunction with the `Object` or `Objects` helpers. It matches the whole Object.

The matcher can accept one of two options, `IgnorePaths` or `AllowPaths`:
* `IgnorePaths` instructs the Matcher to ignore given paths when computing a diff.
* `AllowPaths` instructs the Matcher to restrict its diff to the given paths. If empty the Matcher will look at all paths.

`IgnoreAutogeneratedMetadata` is a premade `IgnorePaths` that contains the paths for all the metadata fields that are commonly set by the client and APIServer.

```go
cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "config", Namespace: "default"}}
expected := &corev1.ConfigMap{
	ObjectMeta: metav1.ObjectMeta{Name: "config", Namespace: "default"},
	Data: map[string]string{"key", "val"},
}
Eventually(k8s.Object(cm)).Should(EqualObject(expected, IgnoreAutogeneratedMetadata))
```

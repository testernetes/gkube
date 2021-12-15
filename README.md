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

The helper can be constucted by provided zero or many options:

* `WithClient` passes a client for interacting with Kubernetes API servers.
* `WithConfig` passes config for initializing REST configs for talking to the Kubernetes API.
* `WithScheme` passes a scheme which contains information associating Go types with Kubernetes groups, versions, and kinds.
* `WithContext` passes a context which carries deadlines, cancellation signals, and other request-scoped values.

Example for using the KubernetesHelper with Openshift Custom Resources:
```go
scheme := runtime.NewScheme()
openshiftapi.Install(scheme)
k8s := NewKubernetesHelper(WithScheme(scheme))

```

### Client Helpers

Create, Delete, DeleteAllOf, Get, List, Patch, PatchStatus, Update wrap their respective client functions by returning a function which can be called by Gomega's Eventually or Consistently. The function returns an error if it occurs.

### Object Helpers

Object and Objects use Get to retrieve a specificed Object or ObjectList, however they are wrapped in a `func() client.Object`
which is passed to a matcher.

When using Object the object's name must be provided and namespace if it is namespaced.

### Client Options

The controller-runtime's client options are also surfaced for convience when gkube is dot imported.
Options can be provided to all compatible helpers, e.g.:
```go
Eventually(k8s.Objects(&corev1.ConfigMapList{}, InNamespace("default"))).Should(WithJSONPath(
	"{.items[*].metadata.name}",
	ContainElement("my-configmap")),
)
```

## Matchers

### WithJSONPath

To be used in conjunction with the `Object` or `Objects` helpers. It transforms the object by the given expression and runs a nested matcher against the result(s).

See Kubernetes documentation for more details and examples for JSONPath: https://kubernetes.io/docs/reference/kubectl/jsonpath/

Example returning a slice which contains all the namespace names in a cluster:
```go
Eventually(k8s.Objects(&corev1.NamespaceList{})).Should(WithJSONPath(
	"{.items[*].metadata.name}",
	ContainElement("my-namespace")),
)
```
Example returning a pod phase
```go
pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "my-pod", Namespace: "default"}}
Eventually(k8s.Object(pod)).WithTimeout(time.Minute).Should(WithJSONPath(
	"{.status.phase}",
	BeEquivalentTo(corev1.PodRunning),
)
```

### EqualObject

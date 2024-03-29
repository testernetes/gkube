# gKube

A Gomega Kubernetes API matcher/assertion library to help make writing tests simpler with less boilerplate logic.
It wraps the controller-runtime dynamic client and includes some pod sub-resource helpers using client-go.

A simple example of creating a namespace and asserting on some fields with JSONPath:
```go
package simple

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/testernetes/gkube"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Simple use of the KubernetesHelper", Ordered, func() {

	var k8s KubernetesHelper
	var cm *corev1.ConfigMap

	BeforeAll(func() {
		k8s = NewKubernetesHelper()
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "simple-example",
				Namespace: "default",
			},
		}
	})

	It("should create a configmap", func(ctx SpecContext) {
		Eventually(k8s.Create).WithContext(ctx).WithArguments(cm).Should(Succeed())
	}, SpecTimeout(time.Minute))

	It("should update the configmap", func(ctx SpecContext) {
		Eventually(k8s.Update).WithContext(ctx).WithArguments(cm, func() error {
			cm.Data = map[string]string{
				"something": "simple",
			}
			return nil
		}).Should(Succeed())
	}, SpecTimeout(time.Minute))

	It("should contain something simple ", func(ctx SpecContext) {
		Eventually(k8s.Object).WithContext(ctx).WithArguments(cm).Should(
			HaveJSONPath("{.data.something}", Equal("simple")),
		)
	}, SpecTimeout(time.Minute))

	AfterAll(func(ctx SpecContext) {
		Eventually(k8s.Delete(ctx, cm)).Should(Succeed())
	}, NodeTimeout(time.Minute))
})

func TestKubernetesHelper(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Simple")
}
```

More examples can be found at [https://github.com/testernetes/gkube-examples](https://github.com/testernetes/gkube-examples)

## Helper

A Kubernetes Helper interface designed to be used with Gomega assertions.

### Options

The helper can be constructed by providing zero or many options:

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

Object and Objects use Get to retrieve a specified Object or ObjectList, however they are wrapped in a `func() client.Object`
which is passed to a matcher.

When using Object the object's name must be provided and namespace if it is namespaced.

### Client Options

The controller-runtime's [client](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/client) options are also surfaced for convenience when gkube is dot imported.
Zero or many options can be provided to all compatible helpers, e.g.:
```go
Eventually(k8s.Objects(&corev1.ConfigMapList{}, InNamespace("default"))).Should(HaveJSONPath(
	"{.items[*].metadata.name}", ContainElement("my-configmap")))

Eventually(k8s.DeleteAllOf(&corev1.Pod{},
	InNamespace("default"), MatchingLabels{"app": "foo"})).Should(Succeed())
```

### Pod Subresource Helpers

These helpers interact with some of the more useful pod subresources.

#### Exec

Executes a command in a running pod, this helper functions similarly to [gexec](https://onsi.github.io/gomega/#gexec-testing-external-processes).
It allows you to assert against the exit code, and stream output into `gbytes.Buffer` to allow you make assertions against output.

```go
Eventually(k8s.Object(pod)).WithTimeout(time.Minute).Should(HaveJSONPath(
	`{.status.phase}`, BeEquivalentTo(corev1.PodRunning)))

execOpts := &corev1.PodExecOptions{
	Container: pod.Spec.Containers[0].Name,
	Command:   []string{"/bin/sh", "-c", "echo hellopod"},
	Stdout:    true,
	Stderr:    true,
}
session, err := k8s.Exec(pod, execOpts, time.Minute, GinkgoWriter, GinkgoWriter)
Expect(err).ShouldNot(HaveOccurred())

Eventually(session).WithTimeout(time.Minute).Should(Exit())
Eventually(session).Should(Say("hellopod"))
```

#### Log

Streams logs from a pod, this helper functions similarly to [gexec](https://onsi.github.io/gomega/#gexec-testing-external-processes). The session will 'exit' when EOF is reached or an error occurs.

```go
Eventually(k8s.Create(pod)).Should(Succeed())
Eventually(k8s.Object(pod)).WithTimeout(time.Minute).Should(HaveJSONPath(
	`{.status.phase}`, BeEquivalentTo(corev1.PodSucceeded)))

logOpts := &corev1.PodLogOptions{
	Follow: false,
	Container: pod.Spec.Containers[0].Name,
}
session, err := k8s.Logs(pod, logOpts, GinkgoWriter)
Expect(err).ShouldNot(HaveOccurred())

Eventually(session).WithTimeout(time.Minute).Should(Exit())
Eventually(session.Out).Should(Say("hellopod"))
```

#### PortForward

Forwards a pod or service port to a local port for testing. Proxy logs and errors can be written to `GinkgoWriter` so that they only output when a test fails.

```go
Eventually(k8s.Create(pod)).Should(Succeed())
Eventually(k8s.Object(pod)).WithTimeout(time.Minute).Should(HaveJSONPath(
        `{.status.phase}`, Equal(corev1.PodPhase(corev1.PodRunning))))

pf, err := k8s.PortForward(pod, []string{"8080:8080"}, GinkgoWriter, GinkgoWriter)
Expect(err).ShouldNot(HaveOccurred())
defer pf.Close()

resp, err := http.Get("http://127.0.0.1:8080")
Expect(err).ShouldNot(HaveOccurred())
defer resp.Body.Close()

body, err := io.ReadAll(resp.Body)
Expect(err).ShouldNot(HaveOccurred())
Expect(body).Should(BeEquivalentTo("helloworld"))
```

## Matchers

### HaveJSONPath

`HaveJSONPath` succeeds if the object has a field specified by the given JSON path that passes a given matcher, it can be used in conjunction with the `Object` or `Objects` helpers.

If only one result is produced from the expression then the results list is flattened for convenience. It's is generally recommended to keep expressions simple and specific to a field. If you need to validate multiple fields, use multiple assertions or the [EqualObject](#EqualObject) matcher.

See Kubernetes JSONPath documentation for more details and examples: https://kubernetes.io/docs/reference/kubectl/jsonpath/

Example asserting a pod phase:
```go
Eventually(k8s.Object(pod)).WithTimeout(time.Minute).Should(HaveJSONPath(
	"{.status.phase}", BeEquivalentTo(corev1.PodRunning),
)
```

Example asserting a specific condition from a deployment:
```go
Eventually(k8s.Object(deployment)).WithTimeout(time.Minute).Should(HaveJSONPath(
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

`IgnoreAutogeneratedMetadata` is a predefined `IgnorePaths` that contains the paths for all the metadata fields that are commonly set by the client and APIServer.

```go
cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "config", Namespace: "default"}}
expected := &corev1.ConfigMap{
	ObjectMeta: metav1.ObjectMeta{Name: "config", Namespace: "default"},
	Data: map[string]string{"key", "val"},
}
Eventually(k8s.Object(cm)).Should(EqualObject(expected, IgnoreAutogeneratedMetadata))
```

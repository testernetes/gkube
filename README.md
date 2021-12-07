# gKube

A Gomega Kubernetes API matcher/assertion library to help make writing tests simpler with less boilerplate logic.

## Helper

A Kubernetes Helper interface designed to be used with Gomega assertions.

## Matchers

### WithJSONPath

To be used in conjunction with the `Object` or `Objects` helpers. It transforms the object by the given expression and runs sa nested matcher against the result(s).

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

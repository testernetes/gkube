package matchers_test

import (
	"fmt"

	. "github.com/matt-simons/gkube/matchers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var _ = Describe("WithJSONPathMatcher", func() {
	When("passed an invalid jsonpath", func() {
		It("should error", func() {
			matcher := NewWithJSONPathMatcher("{^}", Succeed())

			success, err := matcher.Match(&corev1.Pod{})
			Expect(err).Should(HaveOccurred())
			Expect(err).Should(MatchError(
				fmt.Errorf("Transform function failed: JSON Path '{^}' is invalid: unrecognized character in action: U+005E '^'")))
			Expect(success).Should(BeFalse())
		})
	})

	When("passed an jsonpath to invalid location", func() {
		It("should error", func() {
			matcher := NewWithJSONPathMatcher("{.blah}", Succeed())

			success, err := matcher.Match(&corev1.Pod{})
			Expect(err).Should(HaveOccurred())
			Expect(err).Should(MatchError(
				fmt.Errorf("Transform function failed: JSON Path '{.blah}' failed: blah is not found")))
			Expect(success).Should(BeFalse())
		})
	})

	When("passed a valid jsonpath and empty object", func() {
		It("should succeed", func() {
			matcher := NewWithJSONPathMatcher("{.metadata.name}", BeEmpty())

			success, err := matcher.Match(&corev1.Pod{})
			Expect(err).ShouldNot(HaveOccurred())
			Expect(success).Should(BeTrue())
		})
	})

	When("passed a valid jsonpath and unstructured object", func() {
		It("should succeed", func() {
			matcher := NewWithJSONPathMatcher("{.metadata.name}", Equal("test"))

			u := &unstructured.Unstructured{}
			u.SetName("test")
			success, err := matcher.Match(u)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(success).Should(BeTrue())
		})
	})

	When("passed a valid jsonpath to a struct", func() {
		It("should return the struct", func() {
			matcher := NewWithJSONPathMatcher("{.metadata}", MatchFields(IgnoreExtras, Fields{
				"Name":      Equal("my-pod"),
				"Namespace": Equal("namespace"),
				"Labels":    HaveKeyWithValue("hello", "pod"),
			}))

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "namespace",
					Labels: map[string]string{
						"hello": "pod",
					},
				},
			}

			success, err := matcher.Match(pod)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(success).Should(BeTrue())
		})
	})

	When("passed a valid jsonpath to an array", func() {
		It("should return multiple values", func() {
			matcher := NewWithJSONPathMatcher("{.items[*].metadata.name}", HaveLen(3))

			podList := &corev1.PodList{
				Items: []corev1.Pod{
					{},
					{},
					{},
				},
			}

			success, err := matcher.Match(podList)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(success).Should(BeTrue())
		})
	})
})

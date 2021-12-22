package gkube

import (
	"context"
	"time"

	certmgr "github.com/jetstack/cert-manager/pkg/api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gbytes"
	. "github.com/onsi/gomega/gexec"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("KubernetesHelper", func() {
	var k8s *helper
	var opts []HelperOption

	AssertHelperNotNil := func() {
		It("should not have nil fields", func() {
			Expect(k8s).ShouldNot(BeNil())
			Expect(k8s.Config).ShouldNot(BeNil())
			Expect(k8s.Client).ShouldNot(BeNil())
			Expect(k8s.Scheme).ShouldNot(BeNil())
			Expect(k8s.Context).ShouldNot(BeNil())
			Expect(k8s.PodRestInterface).ShouldNot(BeNil())
		})
	}

	JustBeforeEach(func() {
		k8s = newKubernetesHelper(opts...)
	})

	When("execing in a pod", func() {
		var pod *corev1.Pod
		BeforeEach(func() {
			pod = &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Image:   "gcr.io/atlas-kosmos/busybox:latest",
							Name:    "test",
							Command: []string{"/bin/sh"},
							Args:    []string{"-c", "sleep 60"},
						},
					},
				},
			}
		})
		It("should run the given command in the container", func() {
			Eventually(k8s.Create(pod)).Should(Succeed())
			Eventually(k8s.Object(pod)).WithTimeout(time.Minute).Should(WithJSONPath(
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
		})
		AfterEach(func() {
			Eventually(k8s.Delete(pod)).Should(Succeed())
		})
	})

	When("creating a valid helper", func() {

		Context("with no options", func() {
			AssertHelperNotNil()
		})

		Context("with a custom config", func() {
			BeforeEach(func() {
				opts = append(opts, WithConfig(cfg))
			})

			AssertHelperNotNil()
			It("should use it", func() {
				Expect(k8s.Config).Should(Equal(cfg))
			})
		})

		Context("with a custom scheme", func() {
			var s *runtime.Scheme
			BeforeEach(func() {
				s = certmgr.Scheme
				opts = append(opts, WithScheme(s))
			})

			AssertHelperNotNil()
			It("should use it", func() {
				Expect(k8s.Scheme).Should(Equal(s))
			})
		})

		Context("with a custom context", func() {
			var c context.Context
			BeforeEach(func() {
				c = context.WithValue(context.TODO(), "", "")
				opts = append(opts, WithContext(c))
			})

			AssertHelperNotNil()
			It("should use it", func() {
				Expect(k8s.Context).Should(Equal(c))
			})
		})

		Context("with a custom client", func() {
			var c client.Client
			BeforeEach(func() {
				var err error
				c, err = client.New(cfg, client.Options{
					Scheme: scheme.Scheme,
				})
				Expect(err).ShouldNot(HaveOccurred())
				opts = append(opts, WithClient(c))
			})

			AssertHelperNotNil()
			It("should use it", func() {
				Expect(k8s.Client).Should(Equal(c))
			})
		})

	})

})

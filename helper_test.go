package gkube

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

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
			Expect(k8s.PodRestInterface).ShouldNot(BeNil())
		})
	}

	log.SetOutput(GinkgoWriter)
	SetDefaultEventuallyTimeout(time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	JustBeforeEach(func() {
		k8s = newKubernetesHelper(opts...)
	})

	When("proxying traffic from a pod or service", func() {
		var pod *corev1.Pod
		BeforeEach(func() {
			pod = &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hello",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Image:   "gcr.io/google-containers/busybox:latest",
							Name:    "test",
							Command: []string{"/bin/sh"},
							Args:    []string{"-c", "while true; do echo -e \"HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\nhelloworld\" | nc -l -p 8080; done"},
							Ports: []corev1.ContainerPort{
								{
									Name:          "hello",
									ContainerPort: 8080,
								},
							},
						},
					},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			}
		})

		It("should get a response from the container via k8s proxy", func(ctx SpecContext) {
			Eventually(k8s.Create(ctx, pod)).Should(Succeed())
			Eventually(k8s.Object(ctx, pod)).Should(HaveJSONPath(
				`{.status.phase}`, Equal(corev1.PodPhase(corev1.PodRunning))))

			session, err := k8s.ProxyGet(ctx, pod, "http", "8080", "/", nil, GinkgoWriter, GinkgoWriter)
			Expect(err).ShouldNot(HaveOccurred())

			Eventually(session).Should(Exit())
			Eventually(session.Out).Should(Say("helloworld"))
		}, SpecTimeout(time.Minute))

		It("should get a response from the container via k8s portforward", func(ctx SpecContext) {
			pod.Name = "hello2"
			Eventually(k8s.Create(ctx, pod)).Should(Succeed())
			Eventually(k8s.Object(ctx, pod)).Should(HaveJSONPath(
				`{.status.phase}`, Equal(corev1.PodPhase(corev1.PodRunning))))

			pf, err := k8s.PortForward(ctx, pod, []string{"0:8080"}, GinkgoWriter, GinkgoWriter)
			Expect(err).ShouldNot(HaveOccurred())
			defer pf.Close()

			forwardedPorts, _ := pf.GetPorts()
			localPort := forwardedPorts[0].Local

			resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d", localPort))
			Expect(err).ShouldNot(HaveOccurred())
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(body).Should(BeEquivalentTo("helloworld"))

		}, SpecTimeout(time.Minute))

		AfterEach(func(ctx SpecContext) {
			Eventually(k8s.Delete(ctx, pod, GracePeriodSeconds(0))).Should(Succeed())
		}, NodeTimeout(time.Minute))
	})

	When("streaming logs from a pod", func() {
		var pod *corev1.Pod
		BeforeEach(func() {
			pod = &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "log-pod",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Image:   "gcr.io/google-containers/busybox:latest",
							Name:    "test",
							Command: []string{"/bin/sh"},
							Args:    []string{"-c", "echo helloworld; exit 0"},
						},
					},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			}
		})

		It("should run the given command in the container", func(ctx SpecContext) {
			Eventually(k8s.Create(ctx, pod)).Should(Succeed())
			Eventually(k8s.Object(ctx, pod)).Should(HaveJSONPath(
				`{.status.phase}`, BeEquivalentTo(corev1.PodSucceeded)))

			logOpts := &corev1.PodLogOptions{
				Container: pod.Spec.Containers[0].Name,
			}
			session, err := k8s.Logs(ctx, pod, logOpts, GinkgoWriter, GinkgoWriter)
			Expect(err).ShouldNot(HaveOccurred())

			Eventually(session).Should(Exit())
			Eventually(session.Out).Should(Say("helloworld"))
		}, SpecTimeout(9*time.Second))

		AfterEach(func(ctx SpecContext) {
			Eventually(k8s.Delete(ctx, pod, GracePeriodSeconds(0))).Should(Succeed())
		}, NodeTimeout(time.Minute))
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
							Image:   "gcr.io/google-containers/busybox:latest",
							Name:    "test",
							Command: []string{"/bin/sh"},
							Args:    []string{"-c", "sleep 60"},
						},
					},
				},
			}
		})
		It("should run the given command in the container", func(ctx SpecContext) {
			Eventually(k8s.Create(ctx, pod)).Should(Succeed())
			Eventually(k8s.Object(ctx, pod)).WithTimeout(time.Minute).Should(HaveJSONPath(
				`{.status.phase}`, BeEquivalentTo(corev1.PodRunning)))

			session, err := k8s.Exec(ctx, pod, []string{"/bin/sh", "-c", "echo hellopod"}, "test", GinkgoWriter, GinkgoWriter)
			Expect(err).ShouldNot(HaveOccurred())

			Eventually(session).WithTimeout(time.Minute).Should(Exit())
			Eventually(session).Should(Say("hellopod"))
		}, SpecTimeout(time.Minute))

		AfterEach(func(ctx SpecContext) {
			Eventually(k8s.Delete(ctx, pod, GracePeriodSeconds(0))).Should(Succeed())
		}, NodeTimeout(time.Minute))
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
				s = runtime.NewScheme()
				opts = append(opts, WithScheme(s))
			})

			AssertHelperNotNil()
			It("should use it", func() {
				Expect(k8s.Scheme).Should(Equal(s))
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

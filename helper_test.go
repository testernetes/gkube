package gkube

import (
	"context"

	certmgr "github.com/jetstack/cert-manager/pkg/api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

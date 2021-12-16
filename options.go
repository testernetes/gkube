package gkube

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type HelperOption interface {
	ApplyToHelper(*helper)
}

func (h *helper) ApplyOptions(opts []HelperOption) {
	for _, o := range opts {
		o.ApplyToHelper(h)
	}
}

// Client Option
func WithClient(c client.Client) k8sClient {
	return k8sClient{c}
}

type k8sClient struct {
	client.Client
}

func (c k8sClient) ApplyToHelper(opts *helper) {
	opts.Client = c.Client
}

// Config Option
func WithConfig(c *rest.Config) k8sConfig {
	return k8sConfig{c}
}

type k8sConfig struct {
	*rest.Config
}

func (c k8sConfig) ApplyToHelper(opts *helper) {
	opts.Config = c.Config
}

// Scheme Option
func WithScheme(s *runtime.Scheme) k8sScheme {
	return k8sScheme{s}
}

type k8sScheme struct {
	*runtime.Scheme
}

func (s k8sScheme) ApplyToHelper(opts *helper) {
	opts.Scheme = s.Scheme
}

// Context Option
func WithContext(c context.Context) k8sContext {
	return k8sContext{c}
}

type k8sContext struct {
	context.Context
}

func (c k8sContext) ApplyToHelper(opts *helper) {
	opts.Context = c.Context
}

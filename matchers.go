package gkube

import (
	"github.com/matt-simons/gkube/matchers"
	gtypes "github.com/onsi/gomega/types"
	"k8s.io/apimachinery/pkg/runtime"
)

type IgnorePaths = matchers.IgnorePaths
type AllowPaths = matchers.AllowPaths

var IgnoreAutogeneratedMetadata = matchers.IgnoreAutogeneratedMetadata

func EqualObject(original runtime.Object, opts ...matchers.EqualObjectMatchOption) gtypes.GomegaMatcher {
	return matchers.NewEqualObjectMatcher(original, opts...)
}

func WithJSONPath(field string, matcher gtypes.GomegaMatcher) gtypes.GomegaMatcher {
	return matchers.NewWithJSONPathMatcher(field, matcher)
}

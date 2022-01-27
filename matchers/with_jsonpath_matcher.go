package matchers

import (
	"fmt"

	. "github.com/onsi/gomega"
	gtypes "github.com/onsi/gomega/types"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/util/jsonpath"
)

func NewWithJSONPathMatcher(jpath string, matcher gtypes.GomegaMatcher) gtypes.GomegaMatcher {
	return WithTransform(func(obj interface{}) (interface{}, error) {
		j := jsonpath.New("")
		if err := j.Parse(jpath); err != nil {
			return nil, fmt.Errorf("JSON Path '%s' is invalid: %s", jpath, err.Error())
		}

		if u, ok := obj.(*unstructured.Unstructured); ok {
			obj = u.UnstructuredContent()
		}

		results, err := j.FindResults(obj)
		if err != nil {
			return nil, fmt.Errorf("JSON Path '%s' failed: %s", jpath, err.Error())
		}

		values := []interface{}{}
		for i := range results {
			for j := range results[i] {
				values = append(values, results[i][j].Interface())
			}
		}

		// Flatten values if single result
		if len(values) == 1 {
			return values[0], nil
		}
		return values, nil
	}, matcher)
}

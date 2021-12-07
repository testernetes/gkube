package matchers

import (
	"fmt"
	"reflect"

	. "github.com/onsi/gomega"
	gtypes "github.com/onsi/gomega/types"
	"k8s.io/client-go/util/jsonpath"
)

func NewWithJSONPathMatcher(field string, matcher gtypes.GomegaMatcher) gtypes.GomegaMatcher {
	return WithTransform(func(obj interface{}) (interface{}, error) {
		j := jsonpath.New("").AllowMissingKeys(true)
		err := j.Parse(field)
		if err != nil {
			return nil, fmt.Errorf("the supplied JSON Path '%s' is invalid: %s", field, err.Error())
		}

		results, err := j.FindResults(obj)
		if err != nil {
			return nil, err
		}
		if len(results) == 0 {
			return nil, nil
		}

		// Multiple results which have one or many values
		if len(results) > 1 {
			res := make([]interface{}, len(results))
			for i := range results {
				res[i] = getInterfaces(results[i])
			}
			return res, nil
		}

		// A single result which has multiple values
		if len(results[0]) > 1 {
			return getInterfaces(results[0]), nil
		}

		// A single result which has a single value
		if len(results[0]) == 1 {
			return results[0][0].Interface(), nil
		}

		// No results
		return nil, nil
	}, matcher)
}

func getInterfaces(in []reflect.Value) []interface{} {
	out := make([]interface{}, len(in))
	for i := range in {
		out[i] = in[i].Interface()
	}
	return out
}

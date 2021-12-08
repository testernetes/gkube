package matchers

import (
	"fmt"
	"reflect"

	. "github.com/onsi/gomega"
	gtypes "github.com/onsi/gomega/types"
	"k8s.io/client-go/util/jsonpath"
)

func NewWithJSONPathMatcher(jpath string, matcher gtypes.GomegaMatcher) gtypes.GomegaMatcher {
	return WithTransform(func(obj interface{}) (interface{}, error) {
		j := jsonpath.New("")
		err := j.Parse(jpath)
		if err != nil {
			return nil, fmt.Errorf("JSON Path '%s' is invalid: %s", jpath, err.Error())
		}

		results, err := j.FindResults(obj)
		if err != nil {
			return nil, err
		}
		if len(results) == 0 {
			return nil, fmt.Errorf("JSON Path '%s' did not produce any results.", jpath)
		}

		if len(results) == 1 {
			switch len(results[0]) {
			case 0:
				return nil, fmt.Errorf("JSON Path '%s' did not produce any results.", jpath)
			case 1:
				// A single result which has a single value
				return results[0][0].Interface(), nil
			default:
				// A single result which has multiple values
				return getInterfaces(results[0]), nil
			}
		}

		// Multiple results which have one or many values
		res := make([]interface{}, len(results))
		for i := range results {
			res[i] = getInterfaces(results[i])
		}
		return res, nil
	}, matcher)
}

func getInterfaces(in []reflect.Value) []interface{} {
	out := make([]interface{}, len(in))
	for i := range in {
		out[i] = in[i].Interface()
	}
	return out
}

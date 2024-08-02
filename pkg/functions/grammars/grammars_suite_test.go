package grammars_test

import (
	"testing"

	. "github.com/mudler/LocalAI/pkg/functions"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGrammar(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Grammar test suite")
}

func createFunction(field1 string, field2 string, name string, properties map[string]interface{}) map[string]interface{} {
	property := map[string]interface{}{}
	property[field1] = FunctionName{Const: name}
	property[field2] = Argument{
		Type:       "object",
		Properties: properties,
	}
	return property
}

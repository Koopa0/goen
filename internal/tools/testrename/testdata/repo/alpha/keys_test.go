package alpha_test

import "testing"

var keys = map[string]string{
	"alpha/alpha_test.go#TestParseEmpty": "listed",
	"alpha/alpha_test.go#TestSame":       "listed",
	"beta/beta_test.go#TestSame":         "listed",
}

func TestKeys(t *testing.T) { _ = keys }

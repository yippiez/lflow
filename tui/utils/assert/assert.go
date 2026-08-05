// Package assert provides functions to assert a condition in tests
package assert

import (
	"fmt"
	"runtime/debug"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func getErrorMessage(m string, a, b interface{}) string {
	return fmt.Sprintf(`%s.
Actual:
========================
%+v
========================

Expected:
========================
%+v
========================

%s`, m, a, b, string(debug.Stack()))
}

func checkEqual(a, b interface{}, message string) (bool, string) {
	if a == b {
		return true, ""
	}

	var m string
	if len(message) == 0 {
		m = fmt.Sprintf("%v != %v", a, b)
	} else {
		m = message
	}
	errorMessage := getErrorMessage(m, a, b)

	return false, errorMessage
}

// Equal errors a test if the actual does not match the expected
func Equal(t *testing.T, a, b interface{}, message string) {
	ok, m := checkEqual(a, b, message)
	if !ok {
		t.Error(m)
	}
}

// NotEqual fails a test if the actual matches the expected
func NotEqual(t *testing.T, a, b interface{}, message string) {
	ok, m := checkEqual(a, b, message)
	if ok {
		t.Error(m)
	}
}

// DeepEqual fails a test if the actual does not deeply equal the expected
func DeepEqual(t *testing.T, a, b interface{}, message string) {
	if cmp.Equal(a, b) {
		return
	}

	if len(message) == 0 {
		message = fmt.Sprintf("%v != %v", a, b)
	}

	errorMessage := getErrorMessage(message, a, b)
	errorMessage = fmt.Sprintf("%v\n%v", errorMessage, cmp.Diff(a, b))
	t.Error(errorMessage)
}

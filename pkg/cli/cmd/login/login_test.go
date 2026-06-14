package login

import (
	"fmt"
	"testing"

	"github.com/lflow/lflow/pkg/assert"
	"github.com/lflow/lflow/pkg/cli/context"
)

func TestGetServerDisplayURL(t *testing.T) {
	testCases := []struct {
		apiEndpoint string
		expected    string
	}{
		{
			apiEndpoint: "https://lflow.mydomain.com/api",
			expected:    "https://lflow.mydomain.com",
		},
		{
			apiEndpoint: "https://mysubdomain.mydomain.com/lflow/api",
			expected:    "https://mysubdomain.mydomain.com",
		},
		{
			apiEndpoint: "https://lflow.mysubdomain.mydomain.com/api",
			expected:    "https://lflow.mysubdomain.mydomain.com",
		},
		{
			apiEndpoint: "some-string",
			expected:    "",
		},
		{
			apiEndpoint: "",
			expected:    "",
		},
		{
			apiEndpoint: "https://",
			expected:    "",
		},
		{
			apiEndpoint: "https://abc",
			expected:    "https://abc",
		},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("for input %s", tc.apiEndpoint), func(t *testing.T) {
			got := getServerDisplayURL(context.DnoteCtx{APIEndpoint: tc.apiEndpoint})
			assert.Equal(t, got, tc.expected, "result mismatch")
		})
	}
}

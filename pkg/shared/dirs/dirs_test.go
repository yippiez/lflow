package dirs

import (
	"testing"

	"github.com/lflow/lflow/pkg/shared/assert"
)

type envTestCase struct {
	envKey   string
	envVal   string
	got      *string
	expected string
}

func testCustomDirs(t *testing.T, testCases []envTestCase) {
	for _, tc := range testCases {
		t.Setenv(tc.envKey, tc.envVal)

		Reload()

		assert.Equal(t, *tc.got, tc.expected, "result mismatch")
	}
}

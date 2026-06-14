//go:build windows

package dirs

import (
	"path/filepath"
	"testing"

	"github.com/lflow/lflow/pkg/assert"
)

func TestDirs(t *testing.T) {
	home := Home
	assert.NotEqual(t, home, "", "home is empty")

	configHome := filepath.Join(home, ".lflow")
	dataHome := filepath.Join(home, ".lflow")
	cacheHome := filepath.Join(home, ".lflow")

	testCases := []struct {
		got      string
		expected string
	}{
		{
			got:      ConfigHome,
			expected: configHome,
		},
		{
			got:      DataHome,
			expected: dataHome,
		},
		{
			got:      CacheHome,
			expected: cacheHome,
		},
	}

	for _, tc := range testCases {
		assert.Equal(t, tc.got, tc.expected, "result mismatch")
	}
}

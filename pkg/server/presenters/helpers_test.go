package presenters

import (
	"testing"
	"time"

	"github.com/lflow/lflow/pkg/assert"
)

func TestFormatTS(t *testing.T) {
	input := time.Date(2025, 1, 15, 10, 30, 45, 123456789, time.UTC)
	expected := time.Date(2025, 1, 15, 10, 30, 45, 123457000, time.UTC)

	got := FormatTS(input)

	assert.Equal(t, got, expected, "FormatTS mismatch")
}

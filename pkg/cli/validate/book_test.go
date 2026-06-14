package validate

import (
	"fmt"
	"testing"

	"github.com/lflow/lflow/pkg/shared/assert"
)

func TestValidateBookName(t *testing.T) {
	testCases := []struct {
		input    string
		expected error
	}{
		{
			input:    "javascript",
			expected: nil,
		},
		{
			input:    "node.js",
			expected: nil,
		},
		{
			input:    "",
			expected: ErrBookNameEmpty,
		},
		{
			input:    "foo bar",
			expected: ErrBookNameHasSpace,
		},
		{
			input:    "123",
			expected: ErrBookNameNumeric,
		},
		{
			input:    "+123",
			expected: nil,
		},
		{
			input:    "-123",
			expected: nil,
		},
		{
			input:    "+javascript",
			expected: nil,
		},
		{
			input:    "0",
			expected: ErrBookNameNumeric,
		},
		{
			input:    "0333",
			expected: ErrBookNameNumeric,
		},
		{
			input:    " javascript",
			expected: ErrBookNameHasSpace,
		},
		{
			input:    "java script",
			expected: ErrBookNameHasSpace,
		},
		{
			input:    "javascript (1)",
			expected: ErrBookNameHasSpace,
		},
		{
			input:    "javascript ",
			expected: ErrBookNameHasSpace,
		},
		{
			input:    "javascript (1) (2) (3)",
			expected: ErrBookNameHasSpace,
		},

		// multiline
		{
			input:    "\n",
			expected: ErrBookNameMultiline,
		},
		{
			input:    "\n\n",
			expected: ErrBookNameMultiline,
		},
		{
			input:    "foo\n",
			expected: ErrBookNameMultiline,
		},
		{
			input:    "foo\nbar\n",
			expected: ErrBookNameMultiline,
		},
		{
			input:    "foo\nbar\nbaz",
			expected: ErrBookNameMultiline,
		},
		{
			input:    "\r\n",
			expected: ErrBookNameMultiline,
		},
		{
			input:    "\r\n\r\n",
			expected: ErrBookNameMultiline,
		},
		{
			input:    "foo\r\n",
			expected: ErrBookNameMultiline,
		},
		{
			input:    "foo\r\nbar\r\n",
			expected: ErrBookNameMultiline,
		},
		{
			input:    "foo\r\nbar\r\nbaz",
			expected: ErrBookNameMultiline,
		},
		{
			input:    "\n\r\n",
			expected: ErrBookNameMultiline,
		},
		{
			input:    "foo\nbar\r\n",
			expected: ErrBookNameMultiline,
		},

		// reserved book names
		{
			input:    "trash",
			expected: ErrBookNameReserved,
		},
		{
			input:    "conflicts",
			expected: ErrBookNameReserved,
		},
	}

	for _, tc := range testCases {
		actual := BookName(tc.input)

		assert.Equal(t, actual, tc.expected, fmt.Sprintf("result does not match for the input '%s'", tc.input))
	}
}

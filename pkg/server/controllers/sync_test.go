package controllers

import (
	"fmt"
	"net/url"
	"reflect"
	"testing"

	"github.com/lflow/lflow/pkg/shared/assert"
	"github.com/pkg/errors"
)

func TestParseGetSyncFragmentQuery(t *testing.T) {
	testCases := []struct {
		input    string
		afterUSN int
		limit    int
		err      error
	}{
		{
			input:    `after_usn=50&limit=50`,
			afterUSN: 50,
			limit:    50,
			err:      nil,
		},
		{
			input:    `limit=50`,
			afterUSN: 0,
			limit:    50,
			err:      nil,
		},
		{
			input:    `after_usn=50`,
			afterUSN: 50,
			limit:    100,
			err:      nil,
		},
		{
			input:    `after_usn=50&limit=100`,
			afterUSN: 50,
			limit:    100,
			err:      nil,
		},
		{
			input:    "",
			afterUSN: 0,
			limit:    100,
			err:      nil,
		},
		{
			input:    "limit=101",
			afterUSN: 0,
			limit:    0,
			err: &queryParamError{
				key:     "limit",
				value:   "101",
				message: "maximum value is 100",
			},
		},
	}

	for idx, tc := range testCases {
		q, err := url.ParseQuery(tc.input)
		if err != nil {
			t.Fatal(errors.Wrap(err, "parsing test input"))
		}

		afterUSN, limit, err := parseGetSyncFragmentQuery(q)
		ok := reflect.DeepEqual(err, tc.err)
		assert.Equal(t, ok, true, fmt.Sprintf("err mismatch for test case %d. Expected: %+v. Got: %+v", idx, tc.err, err))

		assert.Equal(t, afterUSN, tc.afterUSN, fmt.Sprintf("afterUSN mismatch for test case %d", idx))
		assert.Equal(t, limit, tc.limit, fmt.Sprintf("limit mismatch for test case %d", idx))
	}
}

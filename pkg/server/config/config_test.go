package config

import (
	"fmt"
	"testing"

	"github.com/lflow/lflow/pkg/assert"
	"github.com/pkg/errors"
)

func TestValidate(t *testing.T) {
	testCases := []struct {
		config      Config
		expectedErr error
	}{
		{
			config: Config{
				DBPath:  "test.db",
				BaseURL: "http://mock.url",
				Port:    "3000",
			},
			expectedErr: nil,
		},
		{
			config: Config{
				DBPath:  "",
				BaseURL: "http://mock.url",
				Port:    "3000",
			},
			expectedErr: ErrDBMissingPath,
		},
		{
			config: Config{
				DBPath: "test.db",
			},
			expectedErr: ErrBaseURLInvalid,
		},
		{
			config: Config{
				DBPath:  "test.db",
				BaseURL: "http://mock.url",
			},
			expectedErr: ErrPortInvalid,
		},
	}

	for idx, tc := range testCases {
		t.Run(fmt.Sprintf("test case %d", idx), func(t *testing.T) {
			err := validate(tc.config)

			assert.Equal(t, errors.Cause(err), tc.expectedErr, "error mismatch")
		})
	}
}

package database

import (
	"testing"

	"github.com/lflow/lflow/pkg/assert"
	"github.com/lflow/lflow/pkg/server/log"
	"gorm.io/gorm/logger"
)

func TestGetDBLogLevel(t *testing.T) {
	testCases := []struct {
		name     string
		level    string
		expected logger.LogLevel
	}{
		{
			name:     "debug level maps to Info",
			level:    log.LevelDebug,
			expected: logger.Info,
		},
		{
			name:     "info level maps to Silent",
			level:    log.LevelInfo,
			expected: logger.Silent,
		},
		{
			name:     "warn level maps to Warn",
			level:    log.LevelWarn,
			expected: logger.Warn,
		},
		{
			name:     "error level maps to Error",
			level:    log.LevelError,
			expected: logger.Error,
		},
		{
			name:     "unknown level maps to Silent",
			level:    "unknown",
			expected: logger.Silent,
		},
		{
			name:     "empty string maps to Silent",
			level:    "",
			expected: logger.Silent,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := getDBLogLevel(tc.level)
			assert.Equal(t, result, tc.expected, "log level mismatch")
		})
	}
}

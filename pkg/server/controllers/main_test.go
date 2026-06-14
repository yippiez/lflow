package controllers

import (
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// Set timezone to UTC to match database timestamps
	time.Local = time.UTC

	code := m.Run()
	os.Exit(code)
}

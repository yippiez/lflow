package app

import (
	"fmt"
	"testing"

	"github.com/lflow/lflow/pkg/assert"
	"github.com/lflow/lflow/pkg/server/testutils"
)

func TestSendWelcomeEmail(t *testing.T) {
	emailBackend := testutils.MockEmailbackendImplementation{}
	a := NewTest()
	a.EmailBackend = &emailBackend
	a.BaseURL = "http://example.com"

	if err := a.SendWelcomeEmail("alice@example.com"); err != nil {
		t.Fatal(err, "failed to perform")
	}

	assert.Equalf(t, len(emailBackend.Emails), 1, "email queue count mismatch")
	assert.Equal(t, emailBackend.Emails[0].From, "noreply@example.com", "email sender mismatch")
	assert.DeepEqual(t, emailBackend.Emails[0].To, []string{"alice@example.com"}, "email sender mismatch")

}

func TestSendPasswordResetEmail(t *testing.T) {
	emailBackend := testutils.MockEmailbackendImplementation{}
	a := NewTest()
	a.EmailBackend = &emailBackend
	a.BaseURL = "http://example.com"

	if err := a.SendPasswordResetEmail("alice@example.com", "mockTokenValue"); err != nil {
		t.Fatal(err, "failed to perform")
	}

	assert.Equalf(t, len(emailBackend.Emails), 1, "email queue count mismatch")
	assert.Equal(t, emailBackend.Emails[0].From, "noreply@example.com", "email sender mismatch")
	assert.DeepEqual(t, emailBackend.Emails[0].To, []string{"alice@example.com"}, "email sender mismatch")

}

func TestGetSenderEmail(t *testing.T) {
	testCases := []struct {
		baseURL        string
		expectedSender string
	}{
		{
			baseURL:        "https://www.example.com",
			expectedSender: "noreply@example.com",
		},
		{
			baseURL:        "https://www.example2.com",
			expectedSender: "alice@example2.com",
		},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("base url %s", tc.baseURL), func(t *testing.T) {
		})
	}
}

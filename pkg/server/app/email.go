package app

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/lflow/lflow/pkg/server/mailer"
	"github.com/pkg/errors"
)

var defaultSender = "admin@lflow.app"

// GetSenderEmail returns the sender email
func GetSenderEmail(baseURL, want string) (string, error) {
	addr, err := getNoreplySender(baseURL)
	if err != nil {
		return "", errors.Wrap(err, "getting sender email address")
	}

	return addr, nil
}

func getDomainFromURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", errors.Wrap(err, "parsing url")
	}

	host := u.Hostname()
	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return host, nil
	}
	domain := parts[len(parts)-2] + "." + parts[len(parts)-1]

	return domain, nil
}

func getNoreplySender(baseURL string) (string, error) {
	domain, err := getDomainFromURL(baseURL)
	if err != nil {
		return "", errors.Wrap(err, "parsing base url")
	}

	addr := fmt.Sprintf("noreply@%s", domain)
	return addr, nil
}

// SendWelcomeEmail sends welcome email
func (a *App) SendWelcomeEmail(email string) error {
	from, err := GetSenderEmail(a.BaseURL, defaultSender)
	if err != nil {
		return errors.Wrap(err, "getting the sender email")
	}

	data := mailer.WelcomeTmplData{
		AccountEmail: email,
		BaseURL:      a.BaseURL,
	}

	if err := a.EmailBackend.SendEmail(mailer.EmailTypeWelcome, from, []string{email}, data); err != nil {
		return errors.Wrapf(err, "sending welcome email for %s", email)
	}

	return nil
}

// SendPasswordResetEmail sends password reset email
func (a *App) SendPasswordResetEmail(email, tokenValue string) error {
	if email == "" {
		return ErrEmailRequired
	}

	from, err := GetSenderEmail(a.BaseURL, defaultSender)
	if err != nil {
		return errors.Wrap(err, "getting the sender email")
	}

	data := mailer.EmailResetPasswordTmplData{
		AccountEmail: email,
		Token:        tokenValue,
		BaseURL:      a.BaseURL,
	}

	if err := a.EmailBackend.SendEmail(mailer.EmailTypeResetPassword, from, []string{email}, data); err != nil {
		if errors.Cause(err) == mailer.ErrSMTPNotConfigured {
			return ErrInvalidSMTPConfig
		}

		return errors.Wrapf(err, "sending password reset email for %s", email)
	}

	return nil
}

// SendPasswordResetAlertEmail sends email that notifies users of a password change
func (a *App) SendPasswordResetAlertEmail(email string) error {
	from, err := GetSenderEmail(a.BaseURL, defaultSender)
	if err != nil {
		return errors.Wrap(err, "getting the sender email")
	}

	data := mailer.EmailResetPasswordAlertTmplData{
		AccountEmail: email,
		BaseURL:      a.BaseURL,
	}

	if err := a.EmailBackend.SendEmail(mailer.EmailTypeResetPasswordAlert, from, []string{email}, data); err != nil {
		return errors.Wrapf(err, "sending password reset alert email for %s", email)
	}

	return nil
}

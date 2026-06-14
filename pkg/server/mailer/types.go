package mailer

// EmailResetPasswordTmplData is a template data for reset password emails
type EmailResetPasswordTmplData struct {
	AccountEmail string
	Token        string
	BaseURL      string
}

// EmailResetPasswordAlertTmplData is a template data for reset password emails
type EmailResetPasswordAlertTmplData struct {
	AccountEmail string
	BaseURL      string
}

// WelcomeTmplData is a template data for welcome emails
type WelcomeTmplData struct {
	AccountEmail string
	BaseURL      string
}

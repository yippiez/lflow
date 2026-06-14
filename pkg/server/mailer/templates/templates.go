// Package mailer provides a functionality to send emails
package templates

import "embed"

//go:embed *.txt
var Files embed.FS

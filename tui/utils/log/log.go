package log

import (
	"fmt"
	"os"

	"github.com/fatih/color"
)

const (
	debugEnvName  = "DNOTE_DEBUG"
	debugEnvValue = "1"
)

var (
	// colorRed is a red foreground color
	colorRed = color.New(color.FgRed)
	// colorGreen is a green foreground color
	colorGreen = color.New(color.FgGreen)
	// colorYellow is a yellow foreground color
	colorYellow = color.New(color.FgYellow)
	// colorGray is a gray foreground color
	colorGray = color.New(color.FgHiBlack)
)

var indent = "  "

// Successf prints a success message with optional format verbs
func Successf(msg string, v ...interface{}) {
	fmt.Fprintf(color.Output, "%s%s %s", indent, colorGreen.Sprint("→"), fmt.Sprintf(msg, v...))
}

// Plainf prints a plain message without any prefix symbol. It takes optional format verbs.
func Plainf(msg string, v ...interface{}) {
	fmt.Printf("%s%s", indent, fmt.Sprintf(msg, v...))
}

// Warnf prints a warning message with optional format verbs
func Warnf(msg string, v ...interface{}) {
	fmt.Fprintf(color.Output, "%s%s %s", indent, colorYellow.Sprint("→"), fmt.Sprintf(msg, v...))
}

// Errorf prints an error message with optional format verbs
func Errorf(msg string, v ...interface{}) {
	fmt.Fprintf(color.Output, "%s%s %s", indent, colorRed.Sprint("→"), fmt.Sprintf(msg, v...))
}

// Askf prints an question with optional format verbs. The leading symbol differs in color depending
// on whether the input is masked.
func Askf(msg string, masked bool, v ...interface{}) {
	var symbol string
	if masked {
		symbol = colorGray.Sprint("→")
	} else {
		symbol = colorGreen.Sprint("→")
	}

	fmt.Fprintf(color.Output, "%s%s %s: ", indent, symbol, fmt.Sprintf(msg, v...))
}

// isDebug returns true if debug mode is enabled
func isDebug() bool {
	return os.Getenv(debugEnvName) == debugEnvValue
}

// Debug prints to stderr if DNOTE_DEBUG is set. Diagnostics never go to
// stdout: lflow's output is meant to be piped.
func Debug(msg string, v ...interface{}) {
	if isDebug() {
		fmt.Fprintf(color.Error, "%s %s", colorGray.Sprint("DEBUG:"), fmt.Sprintf(msg, v...))
	}
}

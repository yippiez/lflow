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
	// ColorRed is a red foreground color
	ColorRed = color.New(color.FgRed)
	// ColorGreen is a green foreground color
	ColorGreen = color.New(color.FgGreen)
	// ColorYellow is a yellow foreground color
	ColorYellow = color.New(color.FgYellow)
	// ColorBlue is a blue foreground color
	ColorBlue = color.New(color.FgBlue)
	// ColorGray is a gray foreground color
	ColorGray = color.New(color.FgHiBlack)
)

var indent = "  "

// Info prints information
func Info(msg string) {
	fmt.Fprintf(color.Output, "%s%s %s", indent, ColorGray.Sprint("→"), msg)
}

// Infof prints information with optional format verbs
func Infof(msg string, v ...interface{}) {
	fmt.Fprintf(color.Output, "%s%s %s", indent, ColorGray.Sprint("→"), fmt.Sprintf(msg, v...))
}

// Success prints a success message
func Success(msg string) {
	fmt.Fprintf(color.Output, "%s%s %s", indent, ColorGreen.Sprint("→"), msg)
}

// Successf prints a success message with optional format verbs
func Successf(msg string, v ...interface{}) {
	fmt.Fprintf(color.Output, "%s%s %s", indent, ColorGreen.Sprint("→"), fmt.Sprintf(msg, v...))
}

// Plain prints a plain message without any prefix symbol
func Plain(msg string) {
	fmt.Printf("%s%s", indent, msg)
}

// Plainf prints a plain message without any prefix symbol. It takes optional format verbs.
func Plainf(msg string, v ...interface{}) {
	fmt.Printf("%s%s", indent, fmt.Sprintf(msg, v...))
}

// Warnf prints a warning message with optional format verbs
func Warnf(msg string, v ...interface{}) {
	fmt.Fprintf(color.Output, "%s%s %s", indent, ColorYellow.Sprint("→"), fmt.Sprintf(msg, v...))
}

// Error prints an error message
func Error(msg string) {
	fmt.Fprintf(color.Output, "%s%s %s", indent, ColorRed.Sprint("→"), msg)
}

// Errorf prints an error message with optional format verbs
func Errorf(msg string, v ...interface{}) {
	fmt.Fprintf(color.Output, "%s%s %s", indent, ColorRed.Sprint("→"), fmt.Sprintf(msg, v...))
}

// Printf prints an normal message
func Printf(msg string, v ...interface{}) {
	fmt.Fprintf(color.Output, "%s%s %s", indent, ColorGray.Sprint("→"), fmt.Sprintf(msg, v...))
}

// Askf prints an question with optional format verbs. The leading symbol differs in color depending
// on whether the input is masked.
func Askf(msg string, masked bool, v ...interface{}) {
	var symbol string
	if masked {
		symbol = ColorGray.Sprint("→")
	} else {
		symbol = ColorGreen.Sprint("→")
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
		fmt.Fprintf(color.Error, "%s %s", ColorGray.Sprint("DEBUG:"), fmt.Sprintf(msg, v...))
	}
}

// DebugNewline prints a newline only in debug mode
func DebugNewline() {
	if isDebug() {
		fmt.Fprintln(color.Error)
	}
}

// Package prompt provides utilities for interactive yes/no prompts
package prompt

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// FormatQuestion formats a yes/no question with the appropriate choice indicator
func FormatQuestion(question string, optimistic bool) string {
	choices := "(y/N)"
	if optimistic {
		choices = "(Y/n)"
	}
	return fmt.Sprintf("%s %s", question, choices)
}

// ReadYesNo reads and parses a yes/no response from the given reader.
// Returns true if confirmed, respecting optimistic mode.
// In optimistic mode, empty input is treated as confirmation.
func ReadYesNo(r io.Reader, optimistic bool) (bool, error) {
	reader := bufio.NewReader(r)
	input, err := reader.ReadString('\n')
	if err != nil {
		return false, err
	}

	input = strings.ToLower(strings.TrimSpace(input))
	confirmed := input == "y"

	if optimistic {
		confirmed = confirmed || input == ""
	}

	return confirmed, nil
}

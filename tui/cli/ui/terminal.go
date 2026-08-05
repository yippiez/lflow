package ui

import (
	"bufio"
	"os"
	"strings"

	"github.com/lflow/lflow/tui/utils/log"
	"github.com/lflow/lflow/tui/utils/prompt"
	"github.com/pkg/errors"
)

// Confirm prompts for user input to confirm a choice
func Confirm(question string, optimistic bool) (bool, error) {
	message := prompt.FormatQuestion(question, optimistic)

	// Use log.Askf for colored prompt in CLI
	log.Askf(message, false)

	confirmed, err := prompt.ReadYesNo(os.Stdin, optimistic)
	if err != nil {
		return false, errors.Wrap(err, "Failed to get user input")
	}

	return confirmed, nil
}

// Grab text from stdin content
func ReadStdInput() (string, error) {
	var lines []string

	s := bufio.NewScanner(os.Stdin)
	for s.Scan() {
		lines = append(lines, s.Text())
	}
	err := s.Err()
	if err != nil {
		return "", errors.Wrap(err, "reading pipe")
	}

	return strings.Join(lines, "\n"), nil
}

// Package testutils provides utilities used in tests
package testutils

import (
	"bytes"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkg/errors"
)

// NewDnoteCmd returns a new Dnote command and a pointer to stderr
func NewDnoteCmd(opts RunDnoteCmdOptions, binaryName string, arg ...string) (*exec.Cmd, *bytes.Buffer, *bytes.Buffer, error) {
	var stderr, stdout bytes.Buffer

	binaryPath, err := filepath.Abs(binaryName)
	if err != nil {
		return &exec.Cmd{}, &stderr, &stdout, errors.Wrap(err, "getting the absolute path to the test binary")
	}

	cmd := exec.Command(binaryPath, arg...)
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout

	cmd.Env = opts.Env

	return cmd, &stderr, &stdout, nil
}

// RunDnoteCmdOptions is an option for RunDnoteCmd
type RunDnoteCmdOptions struct {
	Env []string
}

// RunLflowCmd runs a lflow command
func RunDnoteCmd(t *testing.T, opts RunDnoteCmdOptions, binaryName string, arg ...string) string {
	t.Logf("running: %s %s", binaryName, strings.Join(arg, " "))

	cmd, stderr, stdout, err := NewDnoteCmd(opts, binaryName, arg...)
	if err != nil {
		t.Logf("\n%s", stdout)
		t.Fatal(errors.Wrap(err, "getting command").Error())
	}

	cmd.Env = append(cmd.Env, "DNOTE_DEBUG=1")

	if err := cmd.Run(); err != nil {
		t.Logf("\n%s", stdout)
		t.Fatal(errors.Wrapf(err, "running command %s", stderr.String()))
	}

	// Print stdout if and only if test fails later
	t.Logf("\n%s", stdout)

	return stdout.String()
}

// WaitLflowCmd runs a lflow command and passes stdout to the callback.
func WaitDnoteCmd(t *testing.T, opts RunDnoteCmdOptions, runFunc func(io.Reader, io.WriteCloser) error, binaryName string, arg ...string) (string, error) {
	t.Logf("running: %s %s", binaryName, strings.Join(arg, " "))

	binaryPath, err := filepath.Abs(binaryName)
	if err != nil {
		return "", errors.Wrap(err, "getting absolute path to test binary")
	}

	cmd := exec.Command(binaryPath, arg...)
	cmd.Env = opts.Env

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", errors.Wrap(err, "getting stdout pipe")
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", errors.Wrap(err, "getting stdin")
	}
	defer stdin.Close()

	if err = cmd.Start(); err != nil {
		return "", errors.Wrap(err, "starting command")
	}

	var output bytes.Buffer
	tee := io.TeeReader(stdout, &output)

	err = runFunc(tee, stdin)
	if err != nil {
		t.Logf("\n%s", output.String())
		return output.String(), errors.Wrap(err, "running callback")
	}

	io.Copy(&output, stdout)

	if err := cmd.Wait(); err != nil {
		t.Logf("\n%s", output.String())
		return output.String(), errors.Wrapf(err, "command failed: %s", stderr.String())
	}

	t.Logf("\n%s", output.String())
	return output.String(), nil
}

func MustWaitDnoteCmd(t *testing.T, opts RunDnoteCmdOptions, runFunc func(io.Reader, io.WriteCloser) error, binaryName string, arg ...string) string {
	output, err := WaitDnoteCmd(t, opts, runFunc, binaryName, arg...)
	if err != nil {
		t.Fatal(err)
	}

	return output
}

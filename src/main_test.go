package main

import (
	"bytes"
	"errors"
	"testing"
)

func TestRunWritesNodeID(t *testing.T) {
	originalGetNodeID := getNodeID
	getNodeID = func() string { return "node-123" }
	t.Cleanup(func() {
		getNodeID = originalGetNodeID
	})

	var buf bytes.Buffer
	if err := run(&buf); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	if got := buf.String(); got != "node-123\n" {
		t.Fatalf("run() output = %q, want %q", got, "node-123\n")
	}
}

func TestRunReturnsWriteError(t *testing.T) {
	err := run(errorWriter{})
	if err == nil {
		t.Fatal("run() error = nil, want non-nil")
	}
}

func TestMainWritesToStdout(t *testing.T) {
	originalGetNodeID := getNodeID
	originalStdout := stdout
	originalStderr := stderr
	originalExit := exit
	t.Cleanup(func() {
		getNodeID = originalGetNodeID
		stdout = originalStdout
		stderr = originalStderr
		exit = originalExit
	})

	getNodeID = func() string { return "node-123" }

	var out bytes.Buffer
	var errOut bytes.Buffer
	stdout = &out
	stderr = &errOut

	exitCalled := false
	exit = func(code int) {
		exitCalled = true
		t.Fatalf("exit(%d) should not have been called", code)
	}

	main()

	if got := out.String(); got != "node-123\n" {
		t.Fatalf("stdout = %q, want %q", got, "node-123\n")
	}

	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}

	if exitCalled {
		t.Fatal("exit() was called unexpectedly")
	}
}

func TestMainWritesErrorAndExitsOnFailure(t *testing.T) {
	originalGetNodeID := getNodeID
	originalStdout := stdout
	originalStderr := stderr
	originalExit := exit
	t.Cleanup(func() {
		getNodeID = originalGetNodeID
		stdout = originalStdout
		stderr = originalStderr
		exit = originalExit
	})

	getNodeID = func() string { return "node-123" }
	stdout = errorWriter{}

	var errOut bytes.Buffer
	stderr = &errOut

	exitCode := 0
	exit = func(code int) {
		exitCode = code
	}

	main()

	if got := errOut.String(); got != "write failed\n" {
		t.Fatalf("stderr = %q, want %q", got, "write failed\n")
	}

	if exitCode != 1 {
		t.Fatalf("exit code = %d, want %d", exitCode, 1)
	}
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

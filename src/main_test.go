package main

import (
	"bytes"
	"errors"
	"testing"
)

func TestRunWritesNodeID(t *testing.T) {
	t.Parallel()

	original := getNodeID
	getNodeID = func() string { return "node-123" }
	defer func() {
		getNodeID = original
	}()

	var buf bytes.Buffer
	if err := run(&buf); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	if got := buf.String(); got != "node-123\n" {
		t.Fatalf("run() output = %q, want %q", got, "node-123\n")
	}
}

func TestRunReturnsWriteError(t *testing.T) {
	t.Parallel()

	err := run(errorWriter{})
	if err == nil {
		t.Fatal("run() error = nil, want non-nil")
	}
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

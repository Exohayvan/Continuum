package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"continuum/src/version"
)

func TestRun(t *testing.T) {
	restore := stubVersionBumpIO(t)

	var stdout bytes.Buffer
	stdoutWriter = &stdout

	path := filepath.Join(t.TempDir(), "messages.txt")
	if err := os.WriteFile(path, []byte("fix bootstrap retry"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := run([]string{"-file", versionPath(t), "-messages-file", path})
	restore()
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}

	if got := stdout.String(); got != "Bumped patch version to 1.2.4\n" {
		t.Fatalf("stdout = %q, want %q", got, "Bumped patch version to 1.2.4\n")
	}
}

func TestRunNoChange(t *testing.T) {
	restore := stubVersionBumpIO(t)

	var stdout bytes.Buffer
	stdoutWriter = &stdout

	path := filepath.Join(t.TempDir(), "messages.txt")
	if err := os.WriteFile(path, []byte("docs cleanup"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := run([]string{"-file", versionPath(t), "-messages-file", path})
	restore()
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}

	if got := stdout.String(); got != "Version unchanged at 1.2.3\n" {
		t.Fatalf("stdout = %q, want %q", got, "Version unchanged at 1.2.3\n")
	}
}

func TestRunMissingMessagesFileFlag(t *testing.T) {
	if err := run([]string{"-file", "src/version.yaml"}); err == nil {
		t.Fatal("run() error = nil, want missing flag failure")
	}
}

func TestRunFlagParseError(t *testing.T) {
	if err := run([]string{"-bad-flag"}); err == nil {
		t.Fatal("run() error = nil, want flag parse failure")
	}
}

func TestRunReadFileError(t *testing.T) {
	restore := stubVersionBumpIO(t)
	readFile = func(string) ([]byte, error) { return nil, errors.New("read failed") }

	err := run([]string{"-file", versionPath(t), "-messages-file", "messages.txt"})
	restore()
	if err == nil {
		t.Fatal("run() error = nil, want read failure")
	}
}

func TestRunBumpVersionFileError(t *testing.T) {
	restore := stubVersionBumpIO(t)
	bumpVersionFile = func(string, string) (version.Value, version.Bump, bool, error) {
		return version.Value{}, version.BumpNone, false, errors.New("write failed")
	}

	path := filepath.Join(t.TempDir(), "messages.txt")
	if err := os.WriteFile(path, []byte("fix retry"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := run([]string{"-file", versionPath(t), "-messages-file", path})
	restore()
	if err == nil {
		t.Fatal("run() error = nil, want version bump failure")
	}
}

func TestMainWritesErrorAndExits(t *testing.T) {
	restore := stubVersionBumpIO(t)
	defer restore()

	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	var stderr bytes.Buffer
	stderrWriter = &stderr

	exitCode := 0
	exitFunc = func(code int) { exitCode = code }

	os.Args = []string{"versionbump"}

	main()

	if got := stderr.String(); got == "" {
		t.Fatal("stderr = empty, want an error message")
	}

	if exitCode != 1 {
		t.Fatalf("exit code = %d, want %d", exitCode, 1)
	}
}

func TestMainSucceedsWithoutExit(t *testing.T) {
	restore := stubVersionBumpIO(t)
	defer restore()

	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	var stdout bytes.Buffer
	stdoutWriter = &stdout

	exitCode := 0
	exitFunc = func(code int) { exitCode = code }

	path := filepath.Join(t.TempDir(), "messages.txt")
	if err := os.WriteFile(path, []byte("fix bootstrap retry"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	os.Args = []string{"versionbump", "-file", versionPath(t), "-messages-file", path}

	main()

	if got := stdout.String(); got != "Bumped patch version to 1.2.4\n" {
		t.Fatalf("stdout = %q, want %q", got, "Bumped patch version to 1.2.4\n")
	}

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want %d", exitCode, 0)
	}
}

func stubVersionBumpIO(t *testing.T) func() {
	t.Helper()

	originalStdout := stdoutWriter
	originalStderr := stderrWriter
	originalReadFile := readFile
	originalBumpVersionFile := bumpVersionFile

	readFile = os.ReadFile
	bumpVersionFile = func(_ string, messages string) (version.Value, version.Bump, bool, error) {
		switch messages {
		case "fix bootstrap retry":
			return version.Value{Major: 1, Minor: 2, Patch: 4}, version.BumpPatch, true, nil
		case "docs cleanup":
			return version.Value{Major: 1, Minor: 2, Patch: 3}, version.BumpNone, false, nil
		default:
			return version.Value{}, version.BumpNone, false, errors.New("unexpected messages")
		}
	}

	return func() {
		stdoutWriter = originalStdout
		stderrWriter = originalStderr
		readFile = originalReadFile
		bumpVersionFile = originalBumpVersionFile
	}
}

func versionPath(t *testing.T) string {
	t.Helper()

	return filepath.Join(t.TempDir(), "version.yaml")
}

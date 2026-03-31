package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"continuum/src/version"
)

const (
	messagesFileName       = "messages.txt"
	versionFileName        = "version.yaml"
	writeFileErrFormat     = "WriteFile() error = %v"
	messagesFileFlag       = "-messages-file"
	baseVersionFlag        = "-base-version"
	baseVersionValue       = "1.1.0"
	setVersionOutput       = "Set version to 1.2.1\n"
	unchangedVersionOutput = "Version unchanged at 1.1.0\n"
	stdoutMismatchFormat   = "stdout = %q, want %q"
	commitMessagesData     = "feat: add panel shell\x00fix bootstrap retry\x00"
	docsOnlyMessageData    = "docs only note about local setup\x00"
)

func TestRun(t *testing.T) {
	restore := stubVersionBumpIO(t)

	var stdout bytes.Buffer
	stdoutWriter = &stdout

	path := filepath.Join(t.TempDir(), messagesFileName)
	if err := os.WriteFile(path, []byte(commitMessagesData), 0o644); err != nil {
		t.Fatalf(writeFileErrFormat, err)
	}

	err := run([]string{"-file", versionPath(t), messagesFileFlag, path, baseVersionFlag, baseVersionValue})
	restore()
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}

	if got := stdout.String(); got != setVersionOutput {
		t.Fatalf(stdoutMismatchFormat, got, setVersionOutput)
	}
}

func TestRunNoChange(t *testing.T) {
	restore := stubVersionBumpIO(t)

	var stdout bytes.Buffer
	stdoutWriter = &stdout

	path := filepath.Join(t.TempDir(), messagesFileName)
	if err := os.WriteFile(path, []byte(docsOnlyMessageData), 0o644); err != nil {
		t.Fatalf(writeFileErrFormat, err)
	}

	err := run([]string{"-file", versionPath(t), messagesFileFlag, path, baseVersionFlag, baseVersionValue})
	restore()
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}

	if got := stdout.String(); got != unchangedVersionOutput {
		t.Fatalf(stdoutMismatchFormat, got, unchangedVersionOutput)
	}
}

func TestRunMissingMessagesFileFlag(t *testing.T) {
	if err := run([]string{"-file", "src/version.yaml", baseVersionFlag, baseVersionValue}); err == nil {
		t.Fatal("run() error = nil, want missing flag failure")
	}
}

func TestRunMissingBaseVersionFlag(t *testing.T) {
	if err := run([]string{"-file", "src/version.yaml", messagesFileFlag, messagesFileName}); err == nil {
		t.Fatal("run() error = nil, want missing base version failure")
	}
}

func TestRunFlagParseError(t *testing.T) {
	if err := run([]string{"-bad-flag"}); err == nil {
		t.Fatal("run() error = nil, want flag parse failure")
	}
}

func TestRunParseVersionError(t *testing.T) {
	restore := stubVersionBumpIO(t)
	parseVersionString = func(string) (version.Value, error) {
		return version.Value{}, errors.New("bad base version")
	}

	err := run([]string{"-file", versionPath(t), messagesFileFlag, messagesFileName, baseVersionFlag, "invalid"})
	restore()
	if err == nil {
		t.Fatal("run() error = nil, want base version parse failure")
	}
}

func TestRunReadFileError(t *testing.T) {
	restore := stubVersionBumpIO(t)
	readFile = func(string) ([]byte, error) { return nil, errors.New("read failed") }

	err := run([]string{"-file", versionPath(t), messagesFileFlag, messagesFileName, baseVersionFlag, baseVersionValue})
	restore()
	if err == nil {
		t.Fatal("run() error = nil, want read failure")
	}
}

func TestRunSetVersionFileError(t *testing.T) {
	restore := stubVersionBumpIO(t)
	setVersionFile = func(string, version.Value) (bool, error) { return false, errors.New("write failed") }

	path := filepath.Join(t.TempDir(), messagesFileName)
	if err := os.WriteFile(path, []byte(commitMessagesData), 0o644); err != nil {
		t.Fatalf(writeFileErrFormat, err)
	}

	err := run([]string{"-file", versionPath(t), messagesFileFlag, path, baseVersionFlag, baseVersionValue})
	restore()
	if err == nil {
		t.Fatal("run() error = nil, want version write failure")
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

	path := filepath.Join(t.TempDir(), messagesFileName)
	if err := os.WriteFile(path, []byte(commitMessagesData), 0o644); err != nil {
		t.Fatalf(writeFileErrFormat, err)
	}

	os.Args = []string{"versionbump", "-file", versionPath(t), messagesFileFlag, path, baseVersionFlag, baseVersionValue}

	main()

	if got := stdout.String(); got != setVersionOutput {
		t.Fatalf(stdoutMismatchFormat, got, setVersionOutput)
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
	originalParseVersionString := parseVersionString
	originalSplitCommitMessages := splitCommitMessages
	originalCalculateVersion := calculateVersion
	originalSetVersionFile := setVersionFile
	originalSetRuntimeDefaultFile := setRuntimeDefaultFile
	originalExitFunc := exitFunc

	readFile = os.ReadFile
	parseVersionString = version.ParseString
	splitCommitMessages = version.SplitCommitMessages
	calculateVersion = func(base version.Value, messages []string) version.Value {
		if base != (version.Value{Major: 1, Minor: 1, Patch: 0}) {
			return version.Value{}
		}

		switch len(messages) {
		case 1:
			return version.Value{Major: 1, Minor: 1, Patch: 0}
		case 2:
			return version.Value{Major: 1, Minor: 2, Patch: 1}
		default:
			return version.Value{}
		}
	}
	setVersionFile = func(_ string, value version.Value) (bool, error) {
		switch value {
		case (version.Value{Major: 1, Minor: 2, Patch: 1}):
			return true, nil
		case (version.Value{Major: 1, Minor: 1, Patch: 0}):
			return false, nil
		default:
			return false, errors.New("unexpected version")
		}
	}
	setRuntimeDefaultFile = func(path string, value version.Value) error {
		if path != "src/version/runtime_default.go" {
			return errors.New("unexpected runtime version path")
		}
		if value != (version.Value{Major: 1, Minor: 2, Patch: 1}) && value != (version.Value{Major: 1, Minor: 1, Patch: 0}) {
			return errors.New("unexpected runtime version value")
		}
		return nil
	}
	exitFunc = os.Exit

	return func() {
		stdoutWriter = originalStdout
		stderrWriter = originalStderr
		readFile = originalReadFile
		parseVersionString = originalParseVersionString
		splitCommitMessages = originalSplitCommitMessages
		calculateVersion = originalCalculateVersion
		setVersionFile = originalSetVersionFile
		setRuntimeDefaultFile = originalSetRuntimeDefaultFile
		exitFunc = originalExitFunc
	}
}

func versionPath(t *testing.T) string {
	t.Helper()

	return filepath.Join(t.TempDir(), versionFileName)
}

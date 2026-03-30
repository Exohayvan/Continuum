package nodeid

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestBuildFingerprintLinuxUsesExpectedSources(t *testing.T) {
	t.Parallel()

	readCalls := []string{}
	runCalls := []string{}

	read := func(path string) (string, error) {
		readCalls = append(readCalls, path)

		switch path {
		case "/etc/machine-id":
			return "machine-a", nil
		case "/var/lib/dbus/machine-id":
			return "machine-b", nil
		default:
			t.Fatalf("unexpected read path: %s", path)
			return "", nil
		}
	}

	run := func(name string, args ...string) (string, error) {
		runCalls = append(runCalls, name+" "+args[0])
		if name != "cat" || len(args) != 1 || args[0] != "/sys/class/dmi/id/product_uuid" {
			t.Fatalf("unexpected command: %s %v", name, args)
		}

		return "uuid-c", nil
	}

	got := buildFingerprint("linux", read, run, func() (string, error) {
		t.Fatal("hostname fallback should not be used")
		return "", nil
	})

	if got != "machine-a|machine-b|uuid-c" {
		t.Fatalf("buildFingerprint() = %q, want %q", got, "machine-a|machine-b|uuid-c")
	}

	if !reflect.DeepEqual(readCalls, []string{"/etc/machine-id", "/var/lib/dbus/machine-id"}) {
		t.Fatalf("read paths = %v", readCalls)
	}

	if !reflect.DeepEqual(runCalls, []string{"cat /sys/class/dmi/id/product_uuid"}) {
		t.Fatalf("run calls = %v", runCalls)
	}
}

func TestBuildFingerprintUsesFallbackWhenIdentifiersAreMissing(t *testing.T) {
	t.Parallel()

	got := buildFingerprint(
		"linux",
		func(string) (string, error) { return "", errors.New("missing") },
		func(string, ...string) (string, error) { return "", errors.New("missing") },
		func() (string, error) { return "test-host", nil },
	)

	if got != "fallback-os|linux|test-host" {
		t.Fatalf("buildFingerprint() = %q, want %q", got, "fallback-os|linux|test-host")
	}
}

func TestBuildFingerprintUsesUnknownOSMarker(t *testing.T) {
	t.Parallel()

	got := buildFingerprint(
		"freebsd",
		nil,
		nil,
		func() (string, error) {
			t.Fatal("hostname fallback should not be used for unknown OS")
			return "", nil
		},
	)

	if got != "unknown-os|freebsd" {
		t.Fatalf("buildFingerprint() = %q, want %q", got, "unknown-os|freebsd")
	}
}

func TestGetNodeIDHashesFingerprintDeterministically(t *testing.T) {
	t.Parallel()

	got := getNodeID(
		"darwin",
		func(string) (string, error) { return "", nil },
		func(string, ...string) (string, error) { return "platform-uuid", nil },
		func() (string, error) { return "", nil },
	)

	sum := sha256.Sum256([]byte("platform-uuid"))
	want := hex.EncodeToString(sum[:])

	if got != want {
		t.Fatalf("getNodeID() = %q, want %q", got, want)
	}
}

func TestGetNodeIDUsesInjectedRuntimeDependencies(t *testing.T) {
	originalRuntimeGOOS := runtimeGOOS
	originalReadNodeIDFile := readNodeIDFile
	originalRunNodeIDCmd := runNodeIDCmd
	originalReadNodeHost := readNodeHost
	t.Cleanup(func() {
		runtimeGOOS = originalRuntimeGOOS
		readNodeIDFile = originalReadNodeIDFile
		runNodeIDCmd = originalRunNodeIDCmd
		readNodeHost = originalReadNodeHost
	})

	runtimeGOOS = func() string { return "windows" }
	readNodeIDFile = func(string) (string, error) {
		t.Fatal("file reads should not be used for windows node IDs")
		return "", nil
	}
	runNodeIDCmd = func(name string, args ...string) (string, error) {
		switch {
		case name == "wmic" && reflect.DeepEqual(args, []string{"csproduct", "get", "uuid"}):
			return "uuid-a", nil
		case name == "powershell" && reflect.DeepEqual(args, []string{"-command", "Get-WmiObject Win32_ComputerSystemProduct | Select-Object -ExpandProperty UUID"}):
			return "uuid-b", nil
		default:
			t.Fatalf("unexpected command: %s %v", name, args)
			return "", nil
		}
	}
	readNodeHost = func() (string, error) {
		t.Fatal("hostname fallback should not be used")
		return "", nil
	}

	got := GetNodeID()

	sum := sha256.Sum256([]byte("uuid-a|uuid-b"))
	want := hex.EncodeToString(sum[:])

	if got != want {
		t.Fatalf("GetNodeID() = %q, want %q", got, want)
	}
}

func TestFilterEmptyRemovesBlankValues(t *testing.T) {
	t.Parallel()

	got := filterEmpty([]string{"alpha", " ", "\tbeta\t", "", "\n"})
	want := []string{"alpha", "beta"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterEmpty() = %v, want %v", got, want)
	}
}

func TestReadTextFileTrimsWhitespace(t *testing.T) {
	t.Parallel()

	file, err := os.CreateTemp(t.TempDir(), "nodeid-*.txt")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}

	if _, err := file.WriteString("  sample-value \n"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}

	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	got, err := readTextFile(file.Name())
	if err != nil {
		t.Fatalf("readTextFile() error = %v", err)
	}

	if got != "sample-value" {
		t.Fatalf("readTextFile() = %q, want %q", got, "sample-value")
	}
}

func TestReadTextFileReturnsErrorForMissingFile(t *testing.T) {
	t.Parallel()

	_, err := readTextFile(filepathJoin(t.TempDir(), "missing.txt"))
	if err == nil {
		t.Fatal("readTextFile() error = nil, want non-nil")
	}
}

func TestReadOrEmptyReturnsEmptyWhenReaderIsNil(t *testing.T) {
	t.Parallel()

	got := readOrEmpty(nil, "/tmp/unused")
	if got != "" {
		t.Fatalf("readOrEmpty() = %q, want empty string", got)
	}
}

func TestReadOrEmptyReturnsEmptyWhenReaderErrors(t *testing.T) {
	t.Parallel()

	got := readOrEmpty(func(string) (string, error) {
		return "", errors.New("read failed")
	}, "/tmp/unused")
	if got != "" {
		t.Fatalf("readOrEmpty() = %q, want empty string", got)
	}
}

func TestRunCommandTrimsWhitespace(t *testing.T) {
	t.Parallel()

	var (
		name string
		args []string
	)

	if runtime.GOOS == "windows" {
		name = "cmd"
		args = []string{"/C", "echo sample-value"}
	} else {
		name = "sh"
		args = []string{"-c", "printf '  sample-value \\n'"}
	}

	got, err := runCommand(name, args...)
	if err != nil {
		t.Fatalf("runCommand() error = %v", err)
	}

	if got != "sample-value" {
		t.Fatalf("runCommand() = %q, want %q", got, "sample-value")
	}
}

func TestRunCommandReturnsErrorForMissingBinary(t *testing.T) {
	t.Parallel()

	_, err := runCommand("continuum-command-does-not-exist")
	if err == nil {
		t.Fatal("runCommand() error = nil, want non-nil")
	}
}

func TestRunOrEmptyReturnsEmptyWhenRunnerIsNil(t *testing.T) {
	t.Parallel()

	got := runOrEmpty(nil, "unused")
	if got != "" {
		t.Fatalf("runOrEmpty() = %q, want empty string", got)
	}
}

func TestRunOrEmptyReturnsEmptyWhenRunnerErrors(t *testing.T) {
	t.Parallel()

	got := runOrEmpty(func(string, ...string) (string, error) {
		return "", errors.New("run failed")
	}, "unused")
	if got != "" {
		t.Fatalf("runOrEmpty() = %q, want empty string", got)
	}
}

func TestFallbackPartsReturnsBaseWhenHostnameReaderIsNil(t *testing.T) {
	t.Parallel()

	got := fallbackParts("linux", nil)
	want := []string{"fallback-os", "linux"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fallbackParts() = %v, want %v", got, want)
	}
}

func TestFallbackPartsReturnsBaseWhenHostnameErrors(t *testing.T) {
	t.Parallel()

	got := fallbackParts("linux", func() (string, error) {
		return "", errors.New("hostname lookup failed")
	})
	want := []string{"fallback-os", "linux"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fallbackParts() = %v, want %v", got, want)
	}
}

func filepathJoin(parts ...string) string {
	return strings.Join(parts, string(os.PathSeparator))
}

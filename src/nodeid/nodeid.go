package nodeid

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

const darwinUUIDCommand = `ioreg -rd1 -c IOPlatformExpertDevice | awk '/IOPlatformUUID/ { print $3; }'`

type fileReader func(path string) (string, error)
type commandRunner func(name string, args ...string) (string, error)
type hostnameReader func() (string, error)

var (
	runtimeGOOS    = func() string { return runtime.GOOS }
	readNodeIDFile = readTextFile
	runNodeIDCmd   = runCommand
	readNodeHost   = os.Hostname
)

// GetNodeID returns a deterministic machine identifier hashed with SHA-256.
func GetNodeID() string {
	return getNodeID(runtimeGOOS(), readNodeIDFile, runNodeIDCmd, readNodeHost)
}

func getNodeID(goos string, read fileReader, run commandRunner, hostname hostnameReader) string {
	fingerprint := buildFingerprint(goos, read, run, hostname)
	sum := sha256.Sum256([]byte(fingerprint))
	return hex.EncodeToString(sum[:])
}

func buildFingerprint(goos string, read fileReader, run commandRunner, hostname hostnameReader) string {
	var parts []string

	switch goos {
	case "linux":
		parts = []string{
			readOrEmpty(read, "/etc/machine-id"),
			readOrEmpty(read, "/var/lib/dbus/machine-id"),
			runOrEmpty(run, "cat", "/sys/class/dmi/id/product_uuid"),
		}
	case "windows":
		parts = []string{
			runOrEmpty(run, "wmic", "csproduct", "get", "uuid"),
			runOrEmpty(run, "powershell", "-command", "Get-WmiObject Win32_ComputerSystemProduct | Select-Object -ExpandProperty UUID"),
		}
	case "darwin":
		parts = []string{
			runOrEmpty(run, "sh", "-c", darwinUUIDCommand),
		}
	default:
		parts = []string{"unknown-os", goos}
	}

	parts = filterEmpty(parts)
	if len(parts) == 0 {
		parts = fallbackParts(goos, hostname)
	}

	return strings.Join(parts, "|")
}

func fallbackParts(goos string, hostname hostnameReader) []string {
	parts := []string{"fallback-os", goos}

	if hostname == nil {
		return parts
	}

	host, err := hostname()
	if err != nil {
		return parts
	}

	if trimmed := strings.TrimSpace(host); trimmed != "" {
		parts = append(parts, trimmed)
	}

	return parts
}

func readTextFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(data)), nil
}

func runCommand(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(out)), nil
}

func readOrEmpty(read fileReader, path string) string {
	if read == nil {
		return ""
	}

	value, err := read(path)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(value)
}

func runOrEmpty(run commandRunner, name string, args ...string) string {
	if run == nil {
		return ""
	}

	value, err := run(name, args...)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(value)
}

func filterEmpty(input []string) []string {
	out := make([]string, 0, len(input))
	for _, value := range input {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}

	return out
}

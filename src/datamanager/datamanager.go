package datamanager

import (
	"os"
	"path/filepath"
	"strings"
)

const directoryMode = 0o755

var (
	currentExecutable = os.Executable
	createDirectory   = os.MkdirAll
	requiredPaths     = []string{
		"data",
		filepath.Join("data", "cache"),
		filepath.Join("data", "local"),
		filepath.Join("data", "local", "account"),
		filepath.Join("data", "network"),
		filepath.Join("data", "network", "accounts"),
		filepath.Join("data", "network", "peers"),
		filepath.Join("data", "network", "storage"),
		filepath.Join("data", "network", "stats"),
	}
)

// EnsureLayout creates the application's managed data directory structure
// alongside the installed app location.
func EnsureLayout() (string, error) {
	executablePath, err := currentExecutable()
	if err != nil {
		return "", err
	}

	appDir := appDirectory(executablePath)
	for _, relativePath := range requiredPaths {
		if err := createDirectory(filepath.Join(appDir, relativePath), directoryMode); err != nil {
			return "", err
		}
	}

	return filepath.Join(appDir, "data"), nil
}

func appDirectory(executablePath string) string {
	cleanPath := filepath.Clean(executablePath)
	appMarker := ".app" + string(os.PathSeparator)
	appIndex := strings.Index(cleanPath, appMarker)
	if appIndex == -1 {
		return filepath.Dir(cleanPath)
	}

	appBundle := cleanPath[:appIndex+4]
	return filepath.Dir(appBundle)
}

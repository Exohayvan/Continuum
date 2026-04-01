package datamanager

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureLayoutCreatesManagedDirectoriesNextToExecutable(t *testing.T) {
	originalCurrentExecutable := currentExecutable
	originalCreateDirectory := createDirectory
	t.Cleanup(func() {
		currentExecutable = originalCurrentExecutable
		createDirectory = originalCreateDirectory
	})

	root := t.TempDir()
	executablePath := filepath.Join(root, "Continuum")
	currentExecutable = func() (string, error) {
		return executablePath, nil
	}
	createDirectory = os.MkdirAll

	dataPath, err := EnsureLayout()
	if err != nil {
		t.Fatalf("EnsureLayout() error = %v", err)
	}

	wantDataPath := filepath.Join(root, "data")
	if dataPath != wantDataPath {
		t.Fatalf("EnsureLayout() = %q, want %q", dataPath, wantDataPath)
	}

	for _, relativePath := range requiredPaths {
		fullPath := filepath.Join(root, relativePath)
		info, err := os.Stat(fullPath)
		if err != nil {
			t.Fatalf("Stat(%q) error = %v", fullPath, err)
		}
		if !info.IsDir() {
			t.Fatalf("%q is not a directory", fullPath)
		}
	}
}

func TestEnsureLayoutCreatesManagedDirectoriesNextToAppBundle(t *testing.T) {
	originalCurrentExecutable := currentExecutable
	originalCreateDirectory := createDirectory
	t.Cleanup(func() {
		currentExecutable = originalCurrentExecutable
		createDirectory = originalCreateDirectory
	})

	root := t.TempDir()
	executablePath := filepath.Join(root, "Continuum.app", "Contents", "MacOS", "Continuum")
	currentExecutable = func() (string, error) {
		return executablePath, nil
	}
	createDirectory = os.MkdirAll

	dataPath, err := EnsureLayout()
	if err != nil {
		t.Fatalf("EnsureLayout() error = %v", err)
	}

	wantDataPath := filepath.Join(root, "data")
	if dataPath != wantDataPath {
		t.Fatalf("EnsureLayout() = %q, want %q", dataPath, wantDataPath)
	}

	if _, err := os.Stat(filepath.Join(root, "Continuum.app", "data")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("data directory was created inside app bundle: %v", err)
	}
}

func TestEnsureLayoutReturnsExecutableError(t *testing.T) {
	originalCurrentExecutable := currentExecutable
	t.Cleanup(func() {
		currentExecutable = originalCurrentExecutable
	})

	wantErr := errors.New("missing executable")
	currentExecutable = func() (string, error) {
		return "", wantErr
	}

	_, err := EnsureLayout()
	if !errors.Is(err, wantErr) {
		t.Fatalf("EnsureLayout() error = %v, want %v", err, wantErr)
	}
}

func TestEnsureLayoutReturnsDirectoryError(t *testing.T) {
	originalCurrentExecutable := currentExecutable
	originalCreateDirectory := createDirectory
	t.Cleanup(func() {
		currentExecutable = originalCurrentExecutable
		createDirectory = originalCreateDirectory
	})

	currentExecutable = func() (string, error) {
		return filepath.Join(t.TempDir(), "Continuum"), nil
	}

	wantErr := errors.New("mkdir failed")
	createDirectory = func(string, os.FileMode) error {
		return wantErr
	}

	_, err := EnsureLayout()
	if !errors.Is(err, wantErr) {
		t.Fatalf("EnsureLayout() error = %v, want %v", err, wantErr)
	}
}

func TestAppDirectoryUsesExecutableParent(t *testing.T) {
	t.Parallel()

	got := appDirectory(filepath.Join("/tmp", "continuum", "Continuum"))
	want := filepath.Join("/tmp", "continuum")
	if got != want {
		t.Fatalf("appDirectory() = %q, want %q", got, want)
	}
}

func TestAppDirectoryUsesBundleParent(t *testing.T) {
	t.Parallel()

	got := appDirectory(filepath.Join("/Applications", "Continuum.app", "Contents", "MacOS", "Continuum"))
	want := "/Applications"
	if got != want {
		t.Fatalf("appDirectory() = %q, want %q", got, want)
	}
}

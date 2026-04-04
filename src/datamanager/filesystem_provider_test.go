package datamanager

import (
	"errors"
	"testing"
)

const testManagedPath = "managed-path"

func TestFilesystemUsageFromProviderReturnsParseError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("parse failed")
	_, err := filesystemUsageFromProvider("bad-path",
		func(string) (string, error) {
			return "", wantErr
		},
		func(string, *uint64, *uint64, *uint64) error {
			t.Fatal("getDiskFreeSpace called after parse error")
			return nil
		},
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("filesystemUsageFromProvider() error = %v, want %v", err, wantErr)
	}
}

func TestFilesystemUsageFromProviderReturnsDiskUsageError(t *testing.T) {
	t.Parallel()

	const testPath = "disk-path"
	wantErr := errors.New("disk usage failed")
	_, err := filesystemUsageFromProvider(testManagedPath,
		func(path string) (string, error) {
			if path != testManagedPath {
				t.Fatalf("parsePath() path = %q, want %q", path, testManagedPath)
			}
			return testPath, nil
		},
		func(path string, _, _, _ *uint64) error {
			if path != testPath {
				t.Fatalf("getDiskFreeSpace() path = %q, want %q", path, testPath)
			}
			return wantErr
		},
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("filesystemUsageFromProvider() error = %v, want %v", err, wantErr)
	}
}

func TestFilesystemUsageFromProviderReturnsTotalBytes(t *testing.T) {
	t.Parallel()

	const (
		wantPath  = "disk-path"
		wantBytes = uint64(123456789)
	)

	size, err := filesystemUsageFromProvider(testManagedPath,
		func(path string) (string, error) {
			if path != testManagedPath {
				t.Fatalf("parsePath() path = %q, want %q", path, testManagedPath)
			}
			return wantPath, nil
		},
		func(path string, freeBytesAvailable, totalNumberOfBytes, totalNumberOfFreeBytes *uint64) error {
			if path != wantPath {
				t.Fatalf("getDiskFreeSpace() path = %q, want %q", path, wantPath)
			}
			*freeBytesAvailable = 1
			*totalNumberOfBytes = wantBytes
			*totalNumberOfFreeBytes = 2
			return nil
		},
	)
	if err != nil {
		t.Fatalf("filesystemUsageFromProvider() error = %v", err)
	}
	if size != wantBytes {
		t.Fatalf("filesystemUsageFromProvider() = %d, want %d", size, wantBytes)
	}
}

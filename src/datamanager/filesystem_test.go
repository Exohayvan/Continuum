package datamanager

import (
	"errors"
	"testing"

	"github.com/shirou/gopsutil/v4/disk"
)

const testFilesystemPath = "/managed-path"

func TestSystemFilesystemUsageReturnsError(t *testing.T) {
	t.Parallel()

	originalReadFilesystemUsage := readFilesystemUsage
	t.Cleanup(func() {
		readFilesystemUsage = originalReadFilesystemUsage
	})

	wantErr := errors.New("usage failed")
	readFilesystemUsage = func(path string) (*disk.UsageStat, error) {
		if path != testFilesystemPath {
			t.Fatalf("readFilesystemUsage() path = %q, want %q", path, testFilesystemPath)
		}
		return nil, wantErr
	}

	_, err := systemFilesystemUsage(testFilesystemPath)
	if !errors.Is(err, wantErr) {
		t.Fatalf("systemFilesystemUsage() error = %v, want %v", err, wantErr)
	}
}

func TestSystemFilesystemUsageReturnsTotalSize(t *testing.T) {
	t.Parallel()

	originalReadFilesystemUsage := readFilesystemUsage
	t.Cleanup(func() {
		readFilesystemUsage = originalReadFilesystemUsage
	})

	const wantBytes = uint64(123456789)
	readFilesystemUsage = func(path string) (*disk.UsageStat, error) {
		if path != testFilesystemPath {
			t.Fatalf("readFilesystemUsage() path = %q, want %q", path, testFilesystemPath)
		}
		return &disk.UsageStat{Total: wantBytes}, nil
	}

	got, err := systemFilesystemUsage(testFilesystemPath)
	if err != nil {
		t.Fatalf("systemFilesystemUsage() error = %v", err)
	}
	if got != wantBytes {
		t.Fatalf("systemFilesystemUsage() = %d, want %d", got, wantBytes)
	}
}

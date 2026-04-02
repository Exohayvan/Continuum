//go:build !windows

package datamanager

import "testing"

func TestSystemFilesystemUsageReturnsPositiveSize(t *testing.T) {
	t.Parallel()

	size, err := systemFilesystemUsage(t.TempDir())
	if err != nil {
		t.Fatalf("systemFilesystemUsage() error = %v", err)
	}
	if size == 0 {
		t.Fatal("systemFilesystemUsage() = 0, want positive filesystem size")
	}
}

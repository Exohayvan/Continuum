//go:build !windows

package datamanager

import "syscall"

func systemFilesystemUsage(path string) (uint64, error) {
	var stats syscall.Statfs_t
	if err := syscall.Statfs(path, &stats); err != nil {
		return 0, err
	}

	return stats.Blocks * uint64(stats.Bsize), nil
}

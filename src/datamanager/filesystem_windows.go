//go:build windows

package datamanager

import "golang.org/x/sys/windows"

func systemFilesystemUsage(path string) (uint64, error) {
	return filesystemUsageFromProvider(path, windows.UTF16PtrFromString, windows.GetDiskFreeSpaceEx)
}

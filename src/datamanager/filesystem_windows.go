//go:build windows

package datamanager

import "golang.org/x/sys/windows"

func systemFilesystemUsage(path string) (uint64, error) {
	targetPath, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}

	var freeBytesAvailable uint64
	var totalNumberOfBytes uint64
	var totalNumberOfFreeBytes uint64
	if err := windows.GetDiskFreeSpaceEx(targetPath, &freeBytesAvailable, &totalNumberOfBytes, &totalNumberOfFreeBytes); err != nil {
		return 0, err
	}

	return totalNumberOfBytes, nil
}

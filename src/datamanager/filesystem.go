package datamanager

import "github.com/shirou/gopsutil/v4/disk"

var readFilesystemUsage = disk.Usage

func systemFilesystemUsage(path string) (uint64, error) {
	usage, err := readFilesystemUsage(path)
	if err != nil {
		return 0, err
	}

	return usage.Total, nil
}

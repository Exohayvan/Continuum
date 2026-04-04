package datamanager

func filesystemUsageFromProvider[T any](
	path string,
	parsePath func(string) (T, error),
	getDiskFreeSpace func(T, *uint64, *uint64, *uint64) error,
) (uint64, error) {
	targetPath, err := parsePath(path)
	if err != nil {
		return 0, err
	}

	var freeBytesAvailable uint64
	var totalNumberOfBytes uint64
	var totalNumberOfFreeBytes uint64
	if err := getDiskFreeSpace(targetPath, &freeBytesAvailable, &totalNumberOfBytes, &totalNumberOfFreeBytes); err != nil {
		return 0, err
	}

	return totalNumberOfBytes, nil
}

package datamanager

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	directoryMode      = 0o755
	throughputWindow   = time.Second
	bitsPerMegabit     = 1_000_000
	managedPathCurrent = "."
)

var (
	currentExecutable = os.Executable
	createDirectory   = os.MkdirAll
	readManagedFile   = os.ReadFile
	writeManagedFile  = os.WriteFile
	walkManagedPath   = filepath.WalkDir
	statFilesystem    = systemFilesystemUsage
	currentTime       = time.Now
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
	managerState = trackedState{}
)

var errPathEscapesDataRoot = errors.New("path escapes managed data directory")

type transferEvent struct {
	recordedAt time.Time
	readBytes  uint64
	writeBytes uint64
}

type trackedState struct {
	mu        sync.Mutex
	dataPath  string
	transfers []transferEvent
}

// DiskUsage captures the current managed-data size and recent disk throughput.
type DiskUsage struct {
	DataPath     string  `json:"dataPath"`
	DataBytes    uint64  `json:"dataBytes"`
	VolumeBytes  uint64  `json:"volumeBytes"`
	UsagePercent float64 `json:"usagePercent"`
	ReadMbps     float64 `json:"readMbps"`
	WriteMbps    float64 `json:"writeMbps"`
}

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

	dataPath := filepath.Join(appDir, "data")
	managerState.setDataPath(dataPath)
	return dataPath, nil
}

// Snapshot returns the current managed-data size, recent read/write throughput,
// and the percentage of the hosting volume consumed by the managed data.
func Snapshot() (DiskUsage, error) {
	dataPath, err := dataRoot()
	if err != nil {
		return DiskUsage{}, err
	}

	dataBytes, err := directorySize(dataPath)
	if err != nil {
		return DiskUsage{}, err
	}

	volumeBytes, err := statFilesystem(dataPath)
	if err != nil {
		return DiskUsage{}, err
	}

	readBytes, writeBytes := managerState.transferTotals(currentTime())
	usagePercent := 0.0
	if volumeBytes > 0 {
		usagePercent = (float64(dataBytes) / float64(volumeBytes)) * 100
	}

	return DiskUsage{
		DataPath:     dataPath,
		DataBytes:    dataBytes,
		VolumeBytes:  volumeBytes,
		UsagePercent: usagePercent,
		ReadMbps:     bytesPerSecondToMegabits(readBytes, throughputWindow),
		WriteMbps:    bytesPerSecondToMegabits(writeBytes, throughputWindow),
	}, nil
}

// ReadFile reads a file from the managed data directory and records the number
// of bytes read so throughput can be surfaced in the UI later.
func ReadFile(relativePath string) ([]byte, error) {
	fullPath, err := managedPath(relativePath)
	if err != nil {
		return nil, err
	}

	data, err := readManagedFile(fullPath)
	if err != nil {
		return nil, err
	}

	managerState.recordTransfer(transferEvent{
		recordedAt: currentTime(),
		readBytes:  uint64(len(data)),
	})
	return data, nil
}

// WriteFile writes a file inside the managed data directory and records the
// number of bytes written so throughput can be surfaced in the UI later.
func WriteFile(relativePath string, data []byte, perm os.FileMode) error {
	fullPath, err := managedPath(relativePath)
	if err != nil {
		return err
	}

	if err := createDirectory(filepath.Dir(fullPath), directoryMode); err != nil {
		return err
	}

	if err := writeManagedFile(fullPath, data, perm); err != nil {
		return err
	}

	managerState.recordTransfer(transferEvent{
		recordedAt: currentTime(),
		writeBytes: uint64(len(data)),
	})
	return nil
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

func managedPath(relativePath string) (string, error) {
	dataPath, err := dataRoot()
	if err != nil {
		return "", err
	}

	cleanRelativePath := filepath.Clean(relativePath)
	if filepath.IsAbs(cleanRelativePath) {
		return "", errPathEscapesDataRoot
	}
	if cleanRelativePath == ".." || strings.HasPrefix(cleanRelativePath, ".."+string(os.PathSeparator)) {
		return "", errPathEscapesDataRoot
	}

	if cleanRelativePath == managedPathCurrent {
		return dataPath, nil
	}

	return filepath.Join(dataPath, cleanRelativePath), nil
}

func dataRoot() (string, error) {
	if dataPath := managerState.getDataPath(); dataPath != "" {
		return dataPath, nil
	}

	return EnsureLayout()
}

func directorySize(root string) (uint64, error) {
	var total uint64
	err := walkManagedPath(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += uint64(info.Size())
		return nil
	})
	if err != nil {
		return 0, err
	}

	return total, nil
}

func bytesPerSecondToMegabits(totalBytes uint64, window time.Duration) float64 {
	if totalBytes == 0 || window <= 0 {
		return 0
	}

	bitsPerSecond := (float64(totalBytes) * 8) / window.Seconds()
	return bitsPerSecond / bitsPerMegabit
}

func (s *trackedState) setDataPath(dataPath string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.dataPath = dataPath
}

func (s *trackedState) getDataPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.dataPath
}

func (s *trackedState) recordTransfer(event transferEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.transfers = append(s.transfers, event)
	s.trimTransfersLocked(event.recordedAt)
}

func (s *trackedState) transferTotals(now time.Time) (uint64, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.trimTransfersLocked(now)

	var readBytes uint64
	var writeBytes uint64
	for _, event := range s.transfers {
		readBytes += event.readBytes
		writeBytes += event.writeBytes
	}

	return readBytes, writeBytes
}

func (s *trackedState) trimTransfersLocked(now time.Time) {
	cutoff := now.Add(-throughputWindow)
	keepFrom := 0
	for keepFrom < len(s.transfers) && s.transfers[keepFrom].recordedAt.Before(cutoff) {
		keepFrom++
	}

	if keepFrom == 0 {
		return
	}

	s.transfers = append([]transferEvent(nil), s.transfers[keepFrom:]...)
}

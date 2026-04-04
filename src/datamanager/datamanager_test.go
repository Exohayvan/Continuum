package datamanager

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	testManagedFile          = "network/stats/usage.txt"
	testFileData             = "continuum"
	testAppBundle            = "Continuum.app"
	writeFileErrorFormat     = "WriteFile() error = %v"
	writeFileWantErrorFormat = "WriteFile() error = %v, want %v"
)

func applicationsDirPath() string {
	return filepath.Join(string(os.PathSeparator), "Applications")
}

func applicationsBundleBinaryPath() string {
	return filepath.Join(applicationsDirPath(), testAppBundle, "Contents", "MacOS", "Continuum")
}

func applicationsBundlePath() string {
	return filepath.Join(applicationsDirPath(), testAppBundle)
}

func TestEnsureLayoutCreatesManagedDirectoriesNextToExecutable(t *testing.T) {
	resetDataManagerTestState(t)

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

	if got := managerState.getDataPath(); got != wantDataPath {
		t.Fatalf("managed data path = %q, want %q", got, wantDataPath)
	}
}

func TestEnsureLayoutCreatesManagedDirectoriesNextToAppBundle(t *testing.T) {
	resetDataManagerTestState(t)

	root := t.TempDir()
	executablePath := filepath.Join(root, testAppBundle, "Contents", "MacOS", "Continuum")
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

	if _, err := os.Stat(filepath.Join(root, testAppBundle, "data")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("data directory was created inside app bundle: %v", err)
	}
}

func TestEnsureLayoutReturnsExecutableError(t *testing.T) {
	resetDataManagerTestState(t)

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
	resetDataManagerTestState(t)

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

func TestSnapshotReportsManagedUsageAndThroughput(t *testing.T) {
	resetDataManagerTestState(t)

	root := t.TempDir()
	appPath := filepath.Join(root, "Continuum")
	dataPath := filepath.Join(root, "data")
	if err := os.WriteFile(appPath, []byte("binary"), 0o755); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
	}
	if err := os.MkdirAll(filepath.Join(dataPath, "network", "stats"), directoryMode); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	managerState.setDataPath(dataPath)
	currentExecutable = func() (string, error) {
		return appPath, nil
	}
	createDirectory = os.MkdirAll
	readManagedFile = os.ReadFile
	writeManagedFile = os.WriteFile
	walkManagedPath = filepath.WalkDir
	statFilesystem = func(string) (uint64, error) {
		return 1024, nil
	}

	now := time.Unix(1_700_000_000, 0)
	currentTime = func() time.Time {
		return now
	}

	if err := WriteFile(testManagedFile, []byte(testFileData), 0o644); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
	}

	if _, err := ReadFile(testManagedFile); err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	snapshot, err := Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}

	if snapshot.DataPath != dataPath {
		t.Fatalf("Snapshot().DataPath = %q, want %q", snapshot.DataPath, dataPath)
	}
	if snapshot.AppPath != appPath {
		t.Fatalf("Snapshot().AppPath = %q, want %q", snapshot.AppPath, appPath)
	}
	if snapshot.AppBytes != uint64(len("binary")) {
		t.Fatalf("Snapshot().AppBytes = %d, want %d", snapshot.AppBytes, len("binary"))
	}
	if snapshot.DataBytes != uint64(len(testFileData)) {
		t.Fatalf("Snapshot().DataBytes = %d, want %d", snapshot.DataBytes, len(testFileData))
	}
	if snapshot.TotalBytes != uint64(len("binary")+len(testFileData)) {
		t.Fatalf("Snapshot().TotalBytes = %d, want %d", snapshot.TotalBytes, len("binary")+len(testFileData))
	}
	if snapshot.VolumeBytes != 1024 {
		t.Fatalf("Snapshot().VolumeBytes = %d, want %d", snapshot.VolumeBytes, 1024)
	}
	if snapshot.UsagePercent <= 0 {
		t.Fatalf("Snapshot().UsagePercent = %f, want > 0", snapshot.UsagePercent)
	}
	if snapshot.ReadMbps <= 0 {
		t.Fatalf("Snapshot().ReadMbps = %f, want > 0", snapshot.ReadMbps)
	}
	if snapshot.WriteMbps <= 0 {
		t.Fatalf("Snapshot().WriteMbps = %f, want > 0", snapshot.WriteMbps)
	}
}

func TestSnapshotReturnsDirectorySizeError(t *testing.T) {
	resetDataManagerTestState(t)

	currentExecutable = func() (string, error) {
		return filepath.Join(t.TempDir(), "Continuum"), nil
	}
	managerState.setDataPath(t.TempDir())
	wantErr := errors.New("walk failed")
	walkManagedPath = func(string, fs.WalkDirFunc) error {
		return wantErr
	}

	_, err := Snapshot()
	if !errors.Is(err, wantErr) {
		t.Fatalf("Snapshot() error = %v, want %v", err, wantErr)
	}
}

func TestSnapshotReturnsFilesystemError(t *testing.T) {
	resetDataManagerTestState(t)

	root := t.TempDir()
	appPath := filepath.Join(root, "Continuum")
	if err := os.WriteFile(appPath, []byte("binary"), 0o755); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
	}
	currentExecutable = func() (string, error) {
		return appPath, nil
	}
	managerState.setDataPath(root)
	walkManagedPath = filepath.WalkDir
	statFilesystem = func(string) (uint64, error) {
		return 0, errors.New("statfs failed")
	}

	_, err := Snapshot()
	if err == nil || err.Error() != "statfs failed" {
		t.Fatalf("Snapshot() error = %v, want statfs failure", err)
	}
}

func TestSnapshotReturnsExecutableError(t *testing.T) {
	resetDataManagerTestState(t)

	managerState.setDataPath(t.TempDir())
	wantErr := errors.New("missing executable")
	currentExecutable = func() (string, error) {
		return "", wantErr
	}

	_, err := Snapshot()
	if !errors.Is(err, wantErr) {
		t.Fatalf("Snapshot() error = %v, want %v", err, wantErr)
	}
}

func TestReadFileReturnsManagedPathError(t *testing.T) {
	resetDataManagerTestState(t)

	managerState.setDataPath(t.TempDir())

	_, err := ReadFile("../outside")
	if !errors.Is(err, errPathEscapesDataRoot) {
		t.Fatalf("ReadFile() error = %v, want %v", err, errPathEscapesDataRoot)
	}
}

func TestReadFileReturnsUnderlyingError(t *testing.T) {
	resetDataManagerTestState(t)

	managerState.setDataPath(t.TempDir())
	wantErr := errors.New("read failed")
	readManagedFile = func(string) ([]byte, error) {
		return nil, wantErr
	}

	_, err := ReadFile("stats/missing.json")
	if !errors.Is(err, wantErr) {
		t.Fatalf("ReadFile() error = %v, want %v", err, wantErr)
	}
}

func TestWriteFileReturnsManagedPathError(t *testing.T) {
	resetDataManagerTestState(t)

	managerState.setDataPath(t.TempDir())

	err := WriteFile("../outside", []byte("bad"), 0o644)
	if !errors.Is(err, errPathEscapesDataRoot) {
		t.Fatalf(writeFileWantErrorFormat, err, errPathEscapesDataRoot)
	}
}

func TestWriteFileReturnsCreateDirectoryError(t *testing.T) {
	resetDataManagerTestState(t)

	managerState.setDataPath(t.TempDir())
	wantErr := errors.New("mkdir failed")
	createDirectory = func(string, os.FileMode) error {
		return wantErr
	}

	err := WriteFile("stats/usage.json", []byte("test"), 0o644)
	if !errors.Is(err, wantErr) {
		t.Fatalf(writeFileWantErrorFormat, err, wantErr)
	}
}

func TestWriteFileReturnsUnderlyingError(t *testing.T) {
	resetDataManagerTestState(t)

	managerState.setDataPath(t.TempDir())
	createDirectory = os.MkdirAll
	wantErr := errors.New("write failed")
	writeManagedFile = func(string, []byte, os.FileMode) error {
		return wantErr
	}

	err := WriteFile("stats/usage.json", []byte("test"), 0o644)
	if !errors.Is(err, wantErr) {
		t.Fatalf(writeFileWantErrorFormat, err, wantErr)
	}
}

func TestManagedPathRejectsAbsolutePaths(t *testing.T) {
	resetDataManagerTestState(t)

	managerState.setDataPath(t.TempDir())

	absolutePath, err := filepath.Abs(filepath.Join("tmp", "absolute"))
	if err != nil {
		t.Fatalf("filepath.Abs() error = %v", err)
	}

	_, err = managedPath(absolutePath)
	if !errors.Is(err, errPathEscapesDataRoot) {
		t.Fatalf("managedPath() error = %v, want %v", err, errPathEscapesDataRoot)
	}
}

func TestManagedPathReturnsDataRootForCurrentDirectory(t *testing.T) {
	resetDataManagerTestState(t)

	want := t.TempDir()
	managerState.setDataPath(want)

	got, err := managedPath(".")
	if err != nil {
		t.Fatalf("managedPath() error = %v", err)
	}
	if got != want {
		t.Fatalf("managedPath(.) = %q, want %q", got, want)
	}
}

func TestAppDirectoryUsesExecutableParent(t *testing.T) {
	got := appDirectory(filepath.Join("/tmp", "continuum", "Continuum"))
	want := filepath.Join("/tmp", "continuum")
	if got != want {
		t.Fatalf("appDirectory() = %q, want %q", got, want)
	}
}

func TestAppDirectoryUsesBundleParent(t *testing.T) {
	got := appDirectory(applicationsBundleBinaryPath())
	want := applicationsDirPath()
	if got != want {
		t.Fatalf("appDirectory() = %q, want %q", got, want)
	}
}

func TestInstallPathUsesExecutablePathOutsideBundle(t *testing.T) {
	got := installPath(filepath.Join("/tmp", "continuum", "Continuum"))
	want := filepath.Join("/tmp", "continuum", "Continuum")
	if got != want {
		t.Fatalf("installPath() = %q, want %q", got, want)
	}
}

func TestInstallPathUsesBundleRoot(t *testing.T) {
	got := installPath(applicationsBundleBinaryPath())
	want := applicationsBundlePath()
	if got != want {
		t.Fatalf("installPath() = %q, want %q", got, want)
	}
}

func resetDataManagerTestState(t *testing.T) {
	originalCurrentExecutable := currentExecutable
	originalCreateDirectory := createDirectory
	originalReadManagedFile := readManagedFile
	originalWriteManagedFile := writeManagedFile
	originalWalkManagedPath := walkManagedPath
	originalStatFilesystem := statFilesystem
	originalCurrentTime := currentTime

	managerState = trackedState{}
	currentExecutable = os.Executable
	createDirectory = os.MkdirAll
	readManagedFile = os.ReadFile
	writeManagedFile = os.WriteFile
	walkManagedPath = filepath.WalkDir
	statFilesystem = systemFilesystemUsage
	currentTime = time.Now

	t.Cleanup(func() {
		currentExecutable = originalCurrentExecutable
		createDirectory = originalCreateDirectory
		readManagedFile = originalReadManagedFile
		writeManagedFile = originalWriteManagedFile
		walkManagedPath = originalWalkManagedPath
		statFilesystem = originalStatFilesystem
		currentTime = originalCurrentTime
		managerState = trackedState{}
	})
}

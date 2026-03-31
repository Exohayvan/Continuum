package updater

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"continuum/src/version"
)

const releaseJSON = `[
  {
    "tag_name": "v1.6.0",
    "prerelease": false,
    "assets": [
      {
        "name": "continuum-linux-amd64-v1.6.0.tar.gz",
        "browser_download_url": "https://example.test/continuum-linux-amd64-v1.6.0.tar.gz"
      }
    ]
  },
  {
    "tag_name": "v1.7.0-beta.1",
    "prerelease": true,
    "assets": []
  }
]`

func TestBuildAssetName(t *testing.T) {
	t.Parallel()

	if got := buildAssetName("1.5.0", "linux", "amd64"); got != "continuum-linux-amd64-v1.5.0.tar.gz" {
		t.Fatalf("buildAssetName() = %q, want %q", got, "continuum-linux-amd64-v1.5.0.tar.gz")
	}
}

func TestTimeTickerChan(t *testing.T) {
	t.Parallel()

	ticker := timeTicker{Ticker: time.NewTicker(time.Millisecond)}
	defer ticker.Stop()

	if ticker.Chan() == nil {
		t.Fatal("Chan() = nil, want non-nil channel")
	}
}

func TestFindAssetURL(t *testing.T) {
	t.Parallel()

	release := Release{
		TagName: "v1.5.0",
		Assets: []Asset{
			{Name: "continuum-linux-amd64-v1.5.0.tar.gz", BrowserDownloadURL: "https://example.test/linux"},
		},
	}

	got, err := findAssetURL(release, "continuum-linux-amd64-v1.5.0.tar.gz")
	if err != nil {
		t.Fatalf("findAssetURL() error = %v", err)
	}

	if got != "https://example.test/linux" {
		t.Fatalf("findAssetURL() = %q, want %q", got, "https://example.test/linux")
	}
}

func TestFindAssetURLMissing(t *testing.T) {
	t.Parallel()

	_, err := findAssetURL(Release{TagName: "v1.5.0"}, "missing")
	if err == nil {
		t.Fatal("findAssetURL() error = nil, want failure")
	}
}

func TestLatestStableRelease(t *testing.T) {
	t.Parallel()

	current := version.Value{Major: 1, Minor: 5, Patch: 0}
	releases := []Release{
		{TagName: "v1.6.0", Prerelease: false},
		{TagName: "v1.7.0-beta.1", Prerelease: true},
		{TagName: "v1.5.1", Prerelease: false},
	}

	got, shouldUpdate, err := latestStableRelease(current, releases)
	if err != nil {
		t.Fatalf("latestStableRelease() error = %v", err)
	}

	if !shouldUpdate {
		t.Fatal("latestStableRelease() shouldUpdate = false, want true")
	}

	if got.TagName != "v1.6.0" {
		t.Fatalf("latestStableRelease() tag = %q, want %q", got.TagName, "v1.6.0")
	}
}

func TestLatestStableReleaseNoUpdate(t *testing.T) {
	t.Parallel()

	current := version.Value{Major: 1, Minor: 6, Patch: 0}
	got, shouldUpdate, err := latestStableRelease(current, []Release{{TagName: "v1.6.0"}})
	if err != nil {
		t.Fatalf("latestStableRelease() error = %v", err)
	}

	if shouldUpdate {
		t.Fatal("latestStableRelease() shouldUpdate = true, want false")
	}

	if got.TagName != "v1.6.0" {
		t.Fatalf("latestStableRelease() tag = %q, want %q", got.TagName, "v1.6.0")
	}
}

func TestLatestStableReleaseMissing(t *testing.T) {
	t.Parallel()

	_, _, err := latestStableRelease(version.Value{}, []Release{{TagName: "v1.7.0-beta.1", Prerelease: true}})
	if err == nil {
		t.Fatal("latestStableRelease() error = nil, want failure")
	}
}

func TestLatestStableReleaseInvalidStableTag(t *testing.T) {
	t.Parallel()

	_, _, err := latestStableRelease(version.Value{}, []Release{{TagName: "latest", Prerelease: false}})
	if err == nil {
		t.Fatal("latestStableRelease() error = nil, want failure")
	}
}

func TestFetchReleases(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/Exohayvan/Continuum/releases" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/repos/Exohayvan/Continuum/releases")
		}
		if got := r.Header.Get("User-Agent"); got != "continuum-updater" {
			t.Fatalf("User-Agent = %q, want %q", got, "continuum-updater")
		}
		fmt.Fprint(w, releaseJSON)
	}))
	defer server.Close()

	releases, err := fetchReleases(context.Background(), server.URL, server.Client(), RepoOwner, RepoName)
	if err != nil {
		t.Fatalf("fetchReleases() error = %v", err)
	}

	if len(releases) != 2 {
		t.Fatalf("len(fetchReleases()) = %d, want %d", len(releases), 2)
	}
}

func TestFetchReleasesStatusError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer server.Close()

	_, err := fetchReleases(context.Background(), server.URL, server.Client(), RepoOwner, RepoName)
	if err == nil {
		t.Fatal("fetchReleases() error = nil, want status failure")
	}
}

func TestFetchReleasesDecodeError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "{")
	}))
	defer server.Close()

	_, err := fetchReleases(context.Background(), server.URL, server.Client(), RepoOwner, RepoName)
	if err == nil {
		t.Fatal("fetchReleases() error = nil, want decode failure")
	}
}

func TestFetchReleasesInvalidURL(t *testing.T) {
	t.Parallel()

	_, err := fetchReleases(context.Background(), ":", &http.Client{Timeout: time.Second}, RepoOwner, RepoName)
	if err == nil {
		t.Fatal("fetchReleases() error = nil, want request failure")
	}
}

func TestFetchReleasesClientError(t *testing.T) {
	t.Parallel()

	client := &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("network failed")
		}),
	}

	_, err := fetchReleases(context.Background(), "https://example.test", client, RepoOwner, RepoName)
	if err == nil {
		t.Fatal("fetchReleases() error = nil, want client failure")
	}
}

func TestDownloadReleaseAsset(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "release-asset")
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "asset.tar.gz")
	if err := downloadReleaseAsset(context.Background(), server.Client(), server.URL, path); err != nil {
		t.Fatalf("downloadReleaseAsset() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if string(data) != "release-asset" {
		t.Fatalf("downloadReleaseAsset() = %q, want %q", string(data), "release-asset")
	}
}

func TestDownloadReleaseAssetStatusError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusBadGateway)
	}))
	defer server.Close()

	err := downloadReleaseAsset(context.Background(), server.Client(), server.URL, filepath.Join(t.TempDir(), "asset.tar.gz"))
	if err == nil {
		t.Fatal("downloadReleaseAsset() error = nil, want status failure")
	}
}

func TestDownloadReleaseAssetInvalidURL(t *testing.T) {
	t.Parallel()

	err := downloadReleaseAsset(context.Background(), &http.Client{Timeout: time.Second}, ":", filepath.Join(t.TempDir(), "asset.tar.gz"))
	if err == nil {
		t.Fatal("downloadReleaseAsset() error = nil, want request failure")
	}
}

func TestDownloadReleaseAssetClientError(t *testing.T) {
	t.Parallel()

	client := &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("network failed")
		}),
	}

	err := downloadReleaseAsset(context.Background(), client, "https://example.test/asset", filepath.Join(t.TempDir(), "asset.tar.gz"))
	if err == nil {
		t.Fatal("downloadReleaseAsset() error = nil, want client failure")
	}
}

func TestDownloadReleaseAssetOpenError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "asset")
	}))
	defer server.Close()

	dir := t.TempDir()
	if err := downloadReleaseAsset(context.Background(), server.Client(), server.URL, dir); err == nil {
		t.Fatal("downloadReleaseAsset() error = nil, want destination open failure")
	}
}

func TestExtractTarGz(t *testing.T) {
	t.Parallel()

	archive := filepath.Join(t.TempDir(), "archive.tar.gz")
	if err := writeTarGz(archive, map[string]string{
		"Continuum":                "binary",
		"Continuum.app/Info.plist": "plist",
	}); err != nil {
		t.Fatalf("writeTarGz() error = %v", err)
	}

	destination := filepath.Join(t.TempDir(), "extract")
	if err := extractTarGz(archive, destination); err != nil {
		t.Fatalf("extractTarGz() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(destination, "Continuum"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if string(data) != "binary" {
		t.Fatalf("extractTarGz() = %q, want %q", string(data), "binary")
	}
}

func TestExtractTarGzInvalidArchive(t *testing.T) {
	t.Parallel()

	archive := filepath.Join(t.TempDir(), "archive.tar.gz")
	if err := os.WriteFile(archive, []byte("not-gzip"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := extractTarGz(archive, filepath.Join(t.TempDir(), "extract")); err == nil {
		t.Fatal("extractTarGz() error = nil, want gzip failure")
	}
}

func TestExtractTarGzInvalidTarStream(t *testing.T) {
	t.Parallel()

	archive := filepath.Join(t.TempDir(), "archive.tar.gz")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	gzipWriter := gzip.NewWriter(file)
	if _, err := gzipWriter.Write([]byte("not-a-tar-stream")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if err := extractTarGz(archive, filepath.Join(t.TempDir(), "extract")); err == nil {
		t.Fatal("extractTarGz() error = nil, want tar failure")
	}
}

func TestExtractTarGzMissingArchive(t *testing.T) {
	t.Parallel()

	if err := extractTarGz(filepath.Join(t.TempDir(), "missing.tar.gz"), t.TempDir()); err == nil {
		t.Fatal("extractTarGz() error = nil, want open failure")
	}
}

func TestExtractTarGzDirectoryEntry(t *testing.T) {
	t.Parallel()

	archive := filepath.Join(t.TempDir(), "archive.tar.gz")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)

	if err := tarWriter.WriteHeader(&tar.Header{Name: "nested", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatalf("WriteHeader() error = %v", err)
	}
	if err := tarWriter.WriteHeader(&tar.Header{Name: "nested/Continuum", Mode: 0o755, Size: 3}); err != nil {
		t.Fatalf("WriteHeader() error = %v", err)
	}
	if _, err := tarWriter.Write([]byte("bin")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	destination := filepath.Join(t.TempDir(), "extract")
	if err := extractTarGz(archive, destination); err != nil {
		t.Fatalf("extractTarGz() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(destination, "nested")); err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
}

func TestExtractTarGzRejectsTraversal(t *testing.T) {
	t.Parallel()

	archive := filepath.Join(t.TempDir(), "archive.tar.gz")
	if err := writeTarGz(archive, map[string]string{
		"../escape": "bad",
	}); err != nil {
		t.Fatalf("writeTarGz() error = %v", err)
	}

	err := extractTarGz(archive, filepath.Join(t.TempDir(), "extract"))
	if err == nil {
		t.Fatal("extractTarGz() error = nil, want traversal failure")
	}
}

func TestArchiveTargetDestinationRoot(t *testing.T) {
	t.Parallel()

	got, err := archiveTarget("/tmp/continuum", ".")
	if err != nil {
		t.Fatalf("archiveTarget() error = %v", err)
	}

	if got != filepath.Clean("/tmp/continuum") {
		t.Fatalf("archiveTarget() = %q, want %q", got, filepath.Clean("/tmp/continuum"))
	}
}

func TestFindPathByName(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "nested", AppName+".app")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	got, err := findPathByName(root, AppName+".app", true)
	if err != nil {
		t.Fatalf("findPathByName() error = %v", err)
	}

	if got != path {
		t.Fatalf("findPathByName() = %q, want %q", got, path)
	}
}

func TestFindPathByNameMissing(t *testing.T) {
	t.Parallel()

	_, err := findPathByName(t.TempDir(), "missing", false)
	if err == nil {
		t.Fatal("findPathByName() error = nil, want failure")
	}
}

func TestFindPathByNameRootError(t *testing.T) {
	t.Parallel()

	_, err := findPathByName(filepath.Join(t.TempDir(), "missing"), "whatever", false)
	if err == nil {
		t.Fatal("findPathByName() error = nil, want walk failure")
	}
}

func TestAppBundleRoot(t *testing.T) {
	t.Parallel()

	got, err := appBundleRoot("/Applications/Continuum.app/Contents/MacOS/Continuum")
	if err != nil {
		t.Fatalf("appBundleRoot() error = %v", err)
	}

	if got != "/Applications/Continuum.app" {
		t.Fatalf("appBundleRoot() = %q, want %q", got, "/Applications/Continuum.app")
	}
}

func TestAppBundleRootMissing(t *testing.T) {
	t.Parallel()

	_, err := appBundleRoot("/tmp/Continuum")
	if err == nil {
		t.Fatal("appBundleRoot() error = nil, want failure")
	}
}

func TestReplaceUnixBinary(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	current := filepath.Join(root, "Continuum")
	replacement := filepath.Join(root, "replacement", "Continuum")

	if err := os.WriteFile(current, []byte("old"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(replacement), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(replacement, []byte("new"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := replaceUnixBinary(current, replacement)
	if err != nil {
		t.Fatalf("replaceUnixBinary() error = %v", err)
	}

	if got != current {
		t.Fatalf("replaceUnixBinary() = %q, want %q", got, current)
	}

	data, err := os.ReadFile(current)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if string(data) != "new" {
		t.Fatalf("replaceUnixBinary() contents = %q, want %q", string(data), "new")
	}

	if _, err := os.Stat(current + ".bak"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replaceUnixBinary() left a .bak file behind: %v", err)
	}
}

func TestReplaceUnixBinaryRenameFailure(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	renamePath = func(string, string) error { return fmt.Errorf("rename failed") }

	root := t.TempDir()
	current := filepath.Join(root, "Continuum")
	replacement := filepath.Join(root, "replacement", "Continuum")

	if err := os.WriteFile(current, []byte("old"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(replacement), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(replacement, []byte("new"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := replaceUnixBinary(current, replacement); err == nil {
		t.Fatal("replaceUnixBinary() error = nil, want rename failure")
	}
}

func TestReplaceUnixBinaryInitialRenameFailure(t *testing.T) {
	t.Parallel()

	if _, err := replaceUnixBinary("/tmp/current", "/tmp/replacement"); err == nil {
		t.Fatal("replaceUnixBinary() error = nil, want copy failure")
	}
}

func TestReplaceUnixBinaryChmodFailure(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	changeMode = func(string, os.FileMode) error {
		return fmt.Errorf("chmod failed")
	}

	root := t.TempDir()
	current := filepath.Join(root, "Continuum")
	replacement := filepath.Join(root, "replacement", "Continuum")

	if err := os.WriteFile(current, []byte("old"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(replacement), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(replacement, []byte("new"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := replaceUnixBinary(current, replacement); err == nil {
		t.Fatal("replaceUnixBinary() error = nil, want chmod failure")
	}
}

func TestReplaceAppBundle(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	currentBundle := filepath.Join(root, AppName+".app")
	currentExec := filepath.Join(currentBundle, "Contents", "MacOS", AppName)
	replacementBundle := filepath.Join(root, "download", AppName+".app")
	replacementExec := filepath.Join(replacementBundle, "Contents", "MacOS", AppName)

	if err := os.MkdirAll(filepath.Dir(currentExec), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(currentExec, []byte("old"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(replacementExec), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(replacementExec, []byte("new"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := replaceAppBundle(currentExec, replacementBundle)
	if err != nil {
		t.Fatalf("replaceAppBundle() error = %v", err)
	}

	if got != currentExec {
		t.Fatalf("replaceAppBundle() = %q, want %q", got, currentExec)
	}

	data, err := os.ReadFile(currentExec)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if string(data) != "new" {
		t.Fatalf("replaceAppBundle() contents = %q, want %q", string(data), "new")
	}

	if _, err := os.Stat(currentBundle + ".bak"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replaceAppBundle() left a .bak bundle behind: %v", err)
	}
}

func TestReplaceAppBundleFailure(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	renameCalls := 0
	renamePath = func(oldPath string, newPath string) error {
		renameCalls++
		if renameCalls == 2 {
			return fmt.Errorf("rename failed")
		}
		return os.Rename(oldPath, newPath)
	}

	root := t.TempDir()
	currentBundle := filepath.Join(root, AppName+".app")
	currentExec := filepath.Join(currentBundle, "Contents", "MacOS", AppName)
	replacementBundle := filepath.Join(root, "download", AppName+".app")
	replacementExec := filepath.Join(replacementBundle, "Contents", "MacOS", AppName)

	if err := os.MkdirAll(filepath.Dir(currentExec), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(currentExec, []byte("old"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(replacementExec), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(replacementExec, []byte("new"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := replaceAppBundle(currentExec, replacementBundle); err == nil {
		t.Fatal("replaceAppBundle() error = nil, want rename failure")
	}
}

func TestReplaceAppBundleInitialRenameFailure(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	renamePath = func(string, string) error {
		return fmt.Errorf("rename failed")
	}

	currentExec := filepath.Join("/Applications", AppName+".app", "Contents", "MacOS", AppName)
	if _, err := replaceAppBundle(currentExec, "/tmp/download/Continuum.app"); err == nil {
		t.Fatal("replaceAppBundle() error = nil, want initial rename failure")
	}
}

func TestReplaceAppBundleInvalidExecutable(t *testing.T) {
	t.Parallel()

	if _, err := replaceAppBundle("/tmp/Continuum", "/tmp/download/Continuum.app"); err == nil {
		t.Fatal("replaceAppBundle() error = nil, want invalid bundle failure")
	}
}

func TestWindowsUpdateScript(t *testing.T) {
	t.Parallel()

	script := windowsUpdateScript(`C:\Apps\Continuum.exe`, `C:\Temp\Continuum.exe`)
	if !strings.Contains(script, `set "CURRENT=C:\Apps\Continuum.exe"`) {
		t.Fatalf("windowsUpdateScript() missing current path: %q", script)
	}

	if !strings.Contains(script, `start "" "%CURRENT%"`) {
		t.Fatalf("windowsUpdateScript() missing relaunch command: %q", script)
	}

	if strings.Contains(script, `.bak`) {
		t.Fatalf("windowsUpdateScript() should not use .bak paths: %q", script)
	}
}

func TestScheduleWindowsReplacement(t *testing.T) {
	restore := stubUpdaterHooks(t)

	tempDir := t.TempDir()
	current := filepath.Join(tempDir, "Continuum.exe")
	replacement := filepath.Join(tempDir, "download", "Continuum.exe")

	if err := os.MkdirAll(filepath.Dir(replacement), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	called := false
	startOSProcess = func(name string, argv []string, attr *os.ProcAttr) (*os.Process, error) {
		called = true

		if name != "cmd" {
			t.Fatalf("process = %q, want %q", name, "cmd")
		}
		if len(argv) != 3 || argv[2] == "" {
			t.Fatalf("argv = %v, want cmd /C <script>", argv)
		}
		if attr == nil || attr.Dir != filepath.Dir(current) {
			t.Fatalf("Dir = %q, want %q", attr.Dir, filepath.Dir(current))
		}

		return os.FindProcess(os.Getpid())
	}

	if err := scheduleWindowsReplacement(current, replacement); err != nil {
		t.Fatalf("scheduleWindowsReplacement() error = %v", err)
	}

	if !called {
		t.Fatal("scheduleWindowsReplacement() did not start helper process")
	}
	restore()
}

func TestScheduleWindowsReplacementWriteError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	writeTextFile = func(string, []byte, os.FileMode) error {
		return fmt.Errorf("write failed")
	}

	if err := scheduleWindowsReplacement(`C:\Apps\Continuum.exe`, `C:\Temp\Continuum.exe`); err == nil {
		t.Fatal("scheduleWindowsReplacement() error = nil, want write failure")
	}
}

func TestScheduleWindowsReplacementStartError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	startOSProcess = func(string, []string, *os.ProcAttr) (*os.Process, error) {
		return nil, fmt.Errorf("start failed")
	}

	if err := scheduleWindowsReplacement(`C:\Apps\Continuum.exe`, `C:\Temp\Continuum.exe`); err == nil {
		t.Fatal("scheduleWindowsReplacement() error = nil, want start failure")
	}
}

func TestRelaunchBinary(t *testing.T) {
	restore := stubUpdaterHooks(t)

	called := false
	startOSProcess = func(name string, argv []string, attr *os.ProcAttr) (*os.Process, error) {
		called = true

		if name != "/tmp/Continuum" {
			t.Fatalf("process = %q, want %q", name, "/tmp/Continuum")
		}
		if attr == nil || attr.Dir != "/tmp" {
			t.Fatalf("Dir = %q, want %q", attr.Dir, "/tmp")
		}

		return os.FindProcess(os.Getpid())
	}

	if err := relaunchBinary("/tmp/Continuum"); err != nil {
		t.Fatalf("relaunchBinary() error = %v", err)
	}

	if !called {
		t.Fatal("relaunchBinary() did not start the updated binary")
	}
	restore()
}

func TestRelaunchBinaryError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	startOSProcess = func(string, []string, *os.ProcAttr) (*os.Process, error) {
		return nil, fmt.Errorf("start failed")
	}

	if err := relaunchBinary("/tmp/Continuum"); err == nil {
		t.Fatal("relaunchBinary() error = nil, want start failure")
	}
}

func TestStartBackground(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	startOnce = sync.Once{}
	called := 0
	startAsync = func(fn func()) {
		called++
	}

	StartBackground()
	StartBackground()

	if called != 1 {
		t.Fatalf("StartBackground() calls = %d, want %d", called, 1)
	}
}

func TestStartBackgroundRunsLoop(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	startOnce = sync.Once{}
	calls := 0
	tickChannel := make(chan time.Time)
	close(tickChannel)

	startAsync = func(fn func()) { fn() }
	runCheckAndApply = func() error {
		calls++
		return nil
	}
	newLoopTicker = func(time.Duration) loopTicker {
		return fakeTicker{channel: tickChannel}
	}

	StartBackground()

	if calls != 1 {
		t.Fatalf("StartBackground() runCheckAndApply calls = %d, want %d", calls, 1)
	}
}

func TestCheckAndApplyWrapper(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	currentVersion = func() string { return "dev" }
	if err := CheckAndApply(); err != nil {
		t.Fatalf("CheckAndApply() error = %v", err)
	}
}

func TestCheckStatusReportsUpdateRequirement(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, releaseJSON)
	}))
	defer server.Close()

	apiBaseURL = server.URL
	httpClient = server.Client()

	got := checkStatus(context.Background(), "1.5.0")
	if got.CurrentVersion != "1.5.0" {
		t.Fatalf("checkStatus().CurrentVersion = %q, want %q", got.CurrentVersion, "1.5.0")
	}
	if got.RemoteVersion != "v1.6.0" {
		t.Fatalf("checkStatus().RemoteVersion = %q, want %q", got.RemoteVersion, "v1.6.0")
	}
	if !got.UpdateRequired {
		t.Fatal("checkStatus().UpdateRequired = false, want true")
	}
}

func TestCheckStatusSkipsUpdateWhenVersionsMatch(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"tag_name":"v2.0.0","prerelease":false,"assets":[]}]`)
	}))
	defer server.Close()

	apiBaseURL = server.URL
	httpClient = server.Client()

	got := checkStatus(context.Background(), "2.0.0")
	if got.CurrentVersion != "2.0.0" {
		t.Fatalf("checkStatus().CurrentVersion = %q, want %q", got.CurrentVersion, "2.0.0")
	}
	if got.RemoteVersion != "v2.0.0" {
		t.Fatalf("checkStatus().RemoteVersion = %q, want %q", got.RemoteVersion, "v2.0.0")
	}
	if got.UpdateRequired {
		t.Fatal("checkStatus().UpdateRequired = true, want false")
	}
}

func TestCheckStatusSkipsUpdateWhenCurrentIsNewer(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"tag_name":"v2.0.0","prerelease":false,"assets":[]}]`)
	}))
	defer server.Close()

	apiBaseURL = server.URL
	httpClient = server.Client()

	got := checkStatus(context.Background(), "2.0.1")
	if got.CurrentVersion != "2.0.1" {
		t.Fatalf("checkStatus().CurrentVersion = %q, want %q", got.CurrentVersion, "2.0.1")
	}
	if got.RemoteVersion != "v2.0.0" {
		t.Fatalf("checkStatus().RemoteVersion = %q, want %q", got.RemoteVersion, "v2.0.0")
	}
	if got.UpdateRequired {
		t.Fatal("checkStatus().UpdateRequired = true, want false")
	}
}

func TestCheckStatusHandlesUnavailableRemote(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	apiBaseURL = ":"

	got := checkStatus(context.Background(), "1.5.0")
	if got.RemoteVersion != "unavailable" {
		t.Fatalf("checkStatus().RemoteVersion = %q, want %q", got.RemoteVersion, "unavailable")
	}
	if got.UpdateRequired {
		t.Fatal("checkStatus().UpdateRequired = true, want false")
	}
}

func TestCheckStatusSkipsInvalidCurrentVersion(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	got := checkStatus(context.Background(), "dev")
	if got.CurrentVersion != "dev" {
		t.Fatalf("checkStatus().CurrentVersion = %q, want %q", got.CurrentVersion, "dev")
	}
	if got.RemoteVersion != "unavailable" {
		t.Fatalf("checkStatus().RemoteVersion = %q, want %q", got.RemoteVersion, "unavailable")
	}
	if got.UpdateRequired {
		t.Fatal("checkStatus().UpdateRequired = true, want false")
	}
}

func TestRemoteVersionFetchesAndCachesLatest(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, releaseJSON)
	}))
	defer server.Close()

	apiBaseURL = server.URL
	httpClient = server.Client()

	if got := RemoteVersion(); got != "v1.6.0" {
		t.Fatalf("RemoteVersion() = %q, want %q", got, "v1.6.0")
	}

	apiBaseURL = ":"

	if got := RemoteVersion(); got != "v1.6.0" {
		t.Fatalf("RemoteVersion() cached = %q, want %q", got, "v1.6.0")
	}
}

func TestRemoteVersionUnavailableOnFetchError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	apiBaseURL = ":"

	if got := RemoteVersion(); got != "unavailable" {
		t.Fatalf("RemoteVersion() = %q, want %q", got, "unavailable")
	}
}

func TestRunLoop(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	startOnce = sync.Once{}
	calls := 0
	tickChannel := make(chan time.Time, 1)
	tickChannel <- time.Now()
	close(tickChannel)

	var gotInterval time.Duration
	runCheckAndApply = func() error {
		calls++
		return nil
	}
	newLoopTicker = func(interval time.Duration) loopTicker {
		gotInterval = interval
		return fakeTicker{channel: tickChannel}
	}

	runLoop(0)

	if gotInterval != DefaultCheckInterval {
		t.Fatalf("runLoop() interval = %s, want %s", gotInterval, DefaultCheckInterval)
	}

	if calls != 2 {
		t.Fatalf("runLoop() calls = %d, want %d", calls, 2)
	}
}

func TestCheckAndApplySkipsDevBuild(t *testing.T) {
	t.Parallel()

	if err := checkAndApply(context.Background(), "dev", "linux", "amd64"); err != nil {
		t.Fatalf("checkAndApply() error = %v, want nil", err)
	}
}

func TestCheckAndApplyUnix(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	root := t.TempDir()
	current := filepath.Join(root, "Continuum")
	if err := os.WriteFile(current, []byte("old"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	assetName := buildAssetName("v1.6.0", "linux", "amd64")
	server := releaseServer(t, assetName, map[string]string{
		"Continuum": "new",
	})
	defer server.Close()

	apiBaseURL = server.URL
	httpClient = server.Client()
	currentExecutable = func() (string, error) { return current, nil }

	launched := false
	startOSProcess = func(name string, argv []string, attr *os.ProcAttr) (*os.Process, error) {
		launched = true
		if name != current {
			t.Fatalf("process = %q, want %q", name, current)
		}
		return os.FindProcess(os.Getpid())
	}

	if err := checkAndApply(context.Background(), "1.5.0", "linux", "amd64"); err != nil {
		t.Fatalf("checkAndApply() error = %v", err)
	}

	if !launched {
		t.Fatal("checkAndApply() did not relaunch updated binary")
	}

	data, err := os.ReadFile(current)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if string(data) != "new" {
		t.Fatalf("updated contents = %q, want %q", string(data), "new")
	}
}

func TestCheckAndApplyNoStableRelease(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"tag_name":"v2.0.0-beta.1","prerelease":true,"assets":[]}]`)
	}))
	defer server.Close()

	apiBaseURL = server.URL
	httpClient = server.Client()

	if err := checkAndApply(context.Background(), "1.5.0", "linux", "amd64"); err != nil {
		t.Fatalf("checkAndApply() error = %v, want nil", err)
	}
}

func TestCheckAndApplyFetchError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	apiBaseURL = ":"

	if err := checkAndApply(context.Background(), "1.5.0", "linux", "amd64"); err == nil {
		t.Fatal("checkAndApply() error = nil, want fetch failure")
	}
}

func TestCheckAndApplyMissingAsset(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"tag_name":"v1.6.0","prerelease":false,"assets":[]}]`)
	}))
	defer server.Close()

	apiBaseURL = server.URL
	httpClient = server.Client()

	if err := checkAndApply(context.Background(), "1.5.0", "linux", "amd64"); err == nil {
		t.Fatal("checkAndApply() error = nil, want missing asset failure")
	}
}

func TestCheckAndApplyDownloadError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/Exohayvan/Continuum/releases":
			fmt.Fprintf(w, `[{"tag_name":"v1.6.0","prerelease":false,"assets":[{"name":%q,"browser_download_url":%q}]}]`,
				buildAssetName("v1.6.0", "linux", "amd64"),
				server.URL+"/download",
			)
		default:
			http.Error(w, "bad", http.StatusBadGateway)
		}
	}))
	defer server.Close()

	apiBaseURL = server.URL
	httpClient = server.Client()

	if err := checkAndApply(context.Background(), "1.5.0", "linux", "amd64"); err == nil {
		t.Fatal("checkAndApply() error = nil, want download failure")
	}
}

func TestCheckAndApplyWindows(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	root := t.TempDir()
	current := filepath.Join(root, "Continuum.exe")
	if err := os.WriteFile(current, []byte("old"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	assetName := buildAssetName("v1.6.0", "windows", "amd64")
	server := releaseServer(t, assetName, map[string]string{
		"Continuum.exe": "new",
	})
	defer server.Close()

	apiBaseURL = server.URL
	httpClient = server.Client()
	currentExecutable = func() (string, error) { return current, nil }

	started := false
	startOSProcess = func(name string, argv []string, attr *os.ProcAttr) (*os.Process, error) {
		started = true
		if name != "cmd" {
			t.Fatalf("process = %q, want %q", name, "cmd")
		}
		return os.FindProcess(os.Getpid())
	}

	if err := checkAndApply(context.Background(), "1.5.0", "windows", "amd64"); err != nil {
		t.Fatalf("checkAndApply() error = %v", err)
	}

	if !started {
		t.Fatal("checkAndApply() did not launch Windows helper")
	}
}

func TestApplyExtractedUpdateDarwin(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	root := t.TempDir()
	currentExec := filepath.Join(root, AppName+".app", "Contents", "MacOS", AppName)
	replacementExec := filepath.Join(root, "extract", AppName+".app", "Contents", "MacOS", AppName)

	if err := os.MkdirAll(filepath.Dir(currentExec), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(currentExec, []byte("old"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(replacementExec), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(replacementExec, []byte("new"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	currentExecutable = func() (string, error) { return currentExec, nil }

	got, windowsHandoff, err := applyExtractedUpdate("darwin", filepath.Join(root, "extract"))
	if err != nil {
		t.Fatalf("applyExtractedUpdate() error = %v", err)
	}

	if windowsHandoff {
		t.Fatal("applyExtractedUpdate() windowsHandoff = true, want false")
	}

	if got != currentExec {
		t.Fatalf("applyExtractedUpdate() = %q, want %q", got, currentExec)
	}
}

func TestApplyExtractedUpdateCurrentExecutableError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	currentExecutable = func() (string, error) {
		return "", fmt.Errorf("missing executable")
	}

	if _, _, err := applyExtractedUpdate("linux", t.TempDir()); err == nil {
		t.Fatal("applyExtractedUpdate() error = nil, want executable failure")
	}
}

func TestApplyExtractedUpdateMissingReplacement(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	currentExecutable = func() (string, error) { return "/tmp/Continuum", nil }

	if _, _, err := applyExtractedUpdate("linux", t.TempDir()); err == nil {
		t.Fatal("applyExtractedUpdate() error = nil, want missing replacement failure")
	}
}

func stubUpdaterHooks(t *testing.T) func() {
	t.Helper()

	originalAPIBaseURL := apiBaseURL
	originalHTTPClient := httpClient
	originalCurrentExecutable := currentExecutable
	originalCreateTempDir := createTempDir
	originalCreateDirs := createDirs
	originalRemoveAllPaths := removeAllPaths
	originalRemovePath := removePath
	originalRenamePath := renamePath
	originalChangeMode := changeMode
	originalWriteTextFile := writeTextFile
	originalStartOSProcess := startOSProcess
	originalStartAsync := startAsync
	originalRunCheckAndApply := runCheckAndApply
	originalNewLoopTicker := newLoopTicker
	originalCurrentVersion := currentVersion
	originalStartOnce := startOnce
	originalLatestRemoteVersion := latestRemoteVersion

	apiBaseURL = "https://api.github.com"
	httpClient = &http.Client{Timeout: time.Second}
	currentVersion = version.Get
	currentExecutable = os.Executable
	createTempDir = os.MkdirTemp
	createDirs = os.MkdirAll
	removeAllPaths = os.RemoveAll
	removePath = os.Remove
	renamePath = os.Rename
	changeMode = os.Chmod
	writeTextFile = os.WriteFile
	startOSProcess = os.StartProcess
	startAsync = func(fn func()) { go fn() }
	runCheckAndApply = CheckAndApply
	newLoopTicker = func(interval time.Duration) loopTicker {
		return timeTicker{Ticker: time.NewTicker(interval)}
	}
	startOnce = sync.Once{}
	storeRemoteVersion("")

	return func() {
		apiBaseURL = originalAPIBaseURL
		httpClient = originalHTTPClient
		currentVersion = originalCurrentVersion
		currentExecutable = originalCurrentExecutable
		createTempDir = originalCreateTempDir
		createDirs = originalCreateDirs
		removeAllPaths = originalRemoveAllPaths
		removePath = originalRemovePath
		renamePath = originalRenamePath
		changeMode = originalChangeMode
		writeTextFile = originalWriteTextFile
		startOSProcess = originalStartOSProcess
		startAsync = originalStartAsync
		runCheckAndApply = originalRunCheckAndApply
		newLoopTicker = originalNewLoopTicker
		startOnce = originalStartOnce
		storeRemoteVersion(originalLatestRemoteVersion)
	}
}

type fakeTicker struct {
	channel chan time.Time
}

func (f fakeTicker) Chan() <-chan time.Time {
	return f.channel
}

func (f fakeTicker) Stop() {}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func releaseServer(t *testing.T, assetName string, files map[string]string) *httptest.Server {
	t.Helper()

	assetPath := "/download/" + assetName
	releasePayload := fmt.Sprintf(`[
  {
    "tag_name": "v1.6.0",
    "prerelease": false,
    "assets": [
      {
        "name": %q,
        "browser_download_url": %q
      }
    ]
  }
]`, assetName, "{{BASE_URL}}"+assetPath)

	archive := filepath.Join(t.TempDir(), assetName)
	if err := writeTarGz(archive, files); err != nil {
		t.Fatalf("writeTarGz() error = %v", err)
	}

	archiveData, err := os.ReadFile(archive)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/Exohayvan/Continuum/releases":
			payload := strings.ReplaceAll(releasePayload, "{{BASE_URL}}", server.URL)
			fmt.Fprint(w, payload)
		case assetPath:
			_, _ = w.Write(archiveData)
		default:
			http.NotFound(w, r)
		}
	}))

	return server
}

func writeTarGz(path string, files map[string]string) error {
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()

	gzipWriter := gzip.NewWriter(out)
	defer gzipWriter.Close()

	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()

	for name, contents := range files {
		header := &tar.Header{
			Name: name,
			Mode: 0o755,
			Size: int64(len(contents)),
		}

		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if _, err := tarWriter.Write([]byte(contents)); err != nil {
			return err
		}
	}

	return nil
}

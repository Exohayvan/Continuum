package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io/fs"
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

const (
	releaseJSON = `[
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
	linuxAMD64AssetV150            = "continuum-linux-amd64-v1.5.0.tar.gz"
	darwinARM64AssetV160           = "continuum-darwin-arm64-v1.6.0.tar.gz"
	macosARM64AssetV160            = "continuum-macos-arm64-v1.6.0.tar.gz"
	linuxDownloadURL               = "https://example.test/linux"
	stableReleaseTag               = "v1.6.0"
	releasesAPIPath                = "/repos/Exohayvan/Continuum/releases"
	releaseAssetContents           = "release-asset"
	assetArchiveName               = "asset.tar.gz"
	archiveFileName                = "archive.tar.gz"
	readFileErrorFormat            = "ReadFile() error = %v"
	writeTarGzErrorFormat          = "writeTarGz() error = %v"
	writeFileErrorFormat           = "WriteFile() error = %v"
	closeErrorFormat               = "Close() error = %v"
	tmpContinuumDir                = "/tmp/continuum"
	mkdirAllErrorFormat            = "MkdirAll() error = %v"
	tmpContinuumBinary             = "/tmp/Continuum"
	renameFailedError              = "rename failed"
	windowsBinaryName              = "Continuum.exe"
	processMismatchFormat          = "process = %q, want %q"
	checkStatusCurrentFormat       = "checkStatus().CurrentVersion = %q, want %q"
	checkStatusRemoteFormat        = "checkStatus().RemoteVersion = %q, want %q"
	checkStatusErrorFormat         = "checkStatus().UpdateError = %q, want %q"
	stableReleaseTagV200           = "v2.0.0"
	checkStatusUpdateRequiredFalse = "checkStatus().UpdateRequired = true, want false"
	unavailableRemote              = "unavailable"
	updateFailureText              = "update failed"
)

func TestBuildAssetName(t *testing.T) {
	t.Parallel()

	if got := buildAssetName("1.5.0", "linux", "amd64"); got != linuxAMD64AssetV150 {
		t.Fatalf("buildAssetName() = %q, want %q", got, linuxAMD64AssetV150)
	}
}

func TestBuildAssetNamesDarwinIncludesMacOSAlias(t *testing.T) {
	t.Parallel()

	got := buildAssetNames(stableReleaseTag, "darwin", "arm64")
	want := []string{darwinARM64AssetV160, macosARM64AssetV160}

	if len(got) != len(want) {
		t.Fatalf("len(buildAssetNames()) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("buildAssetNames()[%d] = %q, want %q", i, got[i], want[i])
		}
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

func TestStartAsyncDefaultRunsFunction(t *testing.T) {
	done := make(chan struct{})

	startAsyncDefault(func() {
		close(done)
	})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("startAsyncDefault() did not run the function")
	}
}

func TestDefaultLoopTicker(t *testing.T) {
	ticker := defaultLoopTicker(time.Millisecond)
	if ticker == nil {
		t.Fatal("defaultLoopTicker() = nil, want non-nil ticker")
	}

	ticker.Stop()
}

func TestFindAssetURL(t *testing.T) {
	t.Parallel()

	release := Release{
		TagName: "v1.5.0",
		Assets: []Asset{
			{Name: linuxAMD64AssetV150, BrowserDownloadURL: linuxDownloadURL},
		},
	}

	got, err := findAssetURL(release, linuxAMD64AssetV150)
	if err != nil {
		t.Fatalf("findAssetURL() error = %v", err)
	}

	if got != linuxDownloadURL {
		t.Fatalf("findAssetURL() = %q, want %q", got, linuxDownloadURL)
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
		{TagName: stableReleaseTag, Prerelease: false},
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

	if got.TagName != stableReleaseTag {
		t.Fatalf("latestStableRelease() tag = %q, want %q", got.TagName, stableReleaseTag)
	}
}

func TestLatestStableReleaseNoUpdate(t *testing.T) {
	t.Parallel()

	current := version.Value{Major: 1, Minor: 6, Patch: 0}
	got, shouldUpdate, err := latestStableRelease(current, []Release{{TagName: stableReleaseTag}})
	if err != nil {
		t.Fatalf("latestStableRelease() error = %v", err)
	}

	if shouldUpdate {
		t.Fatal("latestStableRelease() shouldUpdate = true, want false")
	}

	if got.TagName != stableReleaseTag {
		t.Fatalf("latestStableRelease() tag = %q, want %q", got.TagName, stableReleaseTag)
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
		if r.URL.Path != releasesAPIPath {
			t.Fatalf("path = %q, want %q", r.URL.Path, releasesAPIPath)
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
		fmt.Fprint(w, releaseAssetContents)
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), assetArchiveName)
	if err := downloadReleaseAsset(context.Background(), server.Client(), server.URL, path); err != nil {
		t.Fatalf("downloadReleaseAsset() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf(readFileErrorFormat, err)
	}

	if string(data) != releaseAssetContents {
		t.Fatalf("downloadReleaseAsset() = %q, want %q", string(data), releaseAssetContents)
	}
}

func TestDownloadReleaseAssetStatusError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusBadGateway)
	}))
	defer server.Close()

	err := downloadReleaseAsset(context.Background(), server.Client(), server.URL, filepath.Join(t.TempDir(), assetArchiveName))
	if err == nil {
		t.Fatal("downloadReleaseAsset() error = nil, want status failure")
	}
}

func TestDownloadReleaseAssetInvalidURL(t *testing.T) {
	t.Parallel()

	err := downloadReleaseAsset(context.Background(), &http.Client{Timeout: time.Second}, ":", filepath.Join(t.TempDir(), assetArchiveName))
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

	err := downloadReleaseAsset(context.Background(), client, "https://example.test/asset", filepath.Join(t.TempDir(), assetArchiveName))
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

func TestDownloadReleaseAssetCreateDirsError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "asset")
	}))
	defer server.Close()

	createDirs = func(string, os.FileMode) error {
		return errors.New("mkdir failed")
	}

	err := downloadReleaseAsset(context.Background(), server.Client(), server.URL, filepath.Join(t.TempDir(), assetArchiveName))
	if err == nil {
		t.Fatal("downloadReleaseAsset() error = nil, want directory creation failure")
	}
}

func TestExtractTarGz(t *testing.T) {
	t.Parallel()

	archive := filepath.Join(t.TempDir(), archiveFileName)
	if err := writeTarGz(archive, map[string]string{
		"Continuum":                "binary",
		"Continuum.app/Info.plist": "plist",
	}); err != nil {
		t.Fatalf(writeTarGzErrorFormat, err)
	}

	destination := filepath.Join(t.TempDir(), "extract")
	if err := extractTarGz(archive, destination); err != nil {
		t.Fatalf("extractTarGz() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(destination, "Continuum"))
	if err != nil {
		t.Fatalf(readFileErrorFormat, err)
	}

	if string(data) != "binary" {
		t.Fatalf("extractTarGz() = %q, want %q", string(data), "binary")
	}
}

func TestExtractTarGzInvalidArchive(t *testing.T) {
	t.Parallel()

	archive := filepath.Join(t.TempDir(), archiveFileName)
	if err := os.WriteFile(archive, []byte("not-gzip"), 0o644); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
	}

	if err := extractTarGz(archive, filepath.Join(t.TempDir(), "extract")); err == nil {
		t.Fatal("extractTarGz() error = nil, want gzip failure")
	}
}

func TestExtractTarGzInvalidTarStream(t *testing.T) {
	t.Parallel()

	archive := filepath.Join(t.TempDir(), archiveFileName)
	file, err := os.Create(archive)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	gzipWriter := gzip.NewWriter(file)
	if _, err := gzipWriter.Write([]byte("not-a-tar-stream")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf(closeErrorFormat, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf(closeErrorFormat, err)
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

	archive := filepath.Join(t.TempDir(), archiveFileName)
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
		t.Fatalf(closeErrorFormat, err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf(closeErrorFormat, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf(closeErrorFormat, err)
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

	archive := filepath.Join(t.TempDir(), archiveFileName)
	if err := writeTarGz(archive, map[string]string{
		"../escape": "bad",
	}); err != nil {
		t.Fatalf(writeTarGzErrorFormat, err)
	}

	err := extractTarGz(archive, filepath.Join(t.TempDir(), "extract"))
	if err == nil {
		t.Fatal("extractTarGz() error = nil, want traversal failure")
	}
}

func TestExtractTarEntryIgnoresUnsupportedType(t *testing.T) {
	if err := extractTarEntry(tar.NewReader(strings.NewReader("")), t.TempDir(), &tar.Header{Name: "ignored", Typeflag: tar.TypeSymlink}); err != nil {
		t.Fatalf("extractTarEntry() error = %v, want nil", err)
	}
}

func TestWriteTarFileCreateDirsError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	createDirs = func(string, os.FileMode) error {
		return errors.New("mkdir failed")
	}

	err := writeTarFile(tar.NewReader(strings.NewReader("")), filepath.Join(t.TempDir(), "nested", "Continuum"), 0o755)
	if err == nil {
		t.Fatal("writeTarFile() error = nil, want directory creation failure")
	}
}

func TestWriteTarFileOpenError(t *testing.T) {
	target := t.TempDir()

	err := writeTarFile(tar.NewReader(strings.NewReader("")), target, 0o755)
	if err == nil {
		t.Fatal("writeTarFile() error = nil, want open failure")
	}
}

func TestWriteTarFileCopyError(t *testing.T) {
	t.Parallel()

	var archive bytes.Buffer
	tarWriter := tar.NewWriter(&archive)
	if err := tarWriter.WriteHeader(&tar.Header{Name: AppName, Mode: 0o755, Size: 5}); err != nil {
		t.Fatalf("WriteHeader() error = %v", err)
	}
	if _, err := archive.WriteString("abc"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}

	tarReader := tar.NewReader(bytes.NewReader(archive.Bytes()))
	if _, err := tarReader.Next(); err != nil {
		t.Fatalf("Next() error = %v", err)
	}

	if err := writeTarFile(tarReader, filepath.Join(t.TempDir(), AppName), 0o755); err == nil {
		t.Fatal("writeTarFile() error = nil, want copy failure")
	}
}

func TestArchiveTargetDestinationRoot(t *testing.T) {
	t.Parallel()

	got, err := archiveTarget(tmpContinuumDir, ".")
	if err != nil {
		t.Fatalf("archiveTarget() error = %v", err)
	}

	if got != filepath.Clean(tmpContinuumDir) {
		t.Fatalf("archiveTarget() = %q, want %q", got, filepath.Clean(tmpContinuumDir))
	}
}

func TestStoreRemoteVersionAddsPrefix(t *testing.T) {
	storeRemoteVersion("")
	storeRemoteVersion("1.6.0")

	if got := cachedRemoteVersion(); got != stableReleaseTag {
		t.Fatalf("cachedRemoteVersion() = %q, want %q", got, stableReleaseTag)
	}
}

func TestFindPathByName(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "nested", AppName+".app")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf(mkdirAllErrorFormat, err)
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

	_, err := appBundleRoot(tmpContinuumBinary)
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
		t.Fatalf(writeFileErrorFormat, err)
	}
	if err := os.MkdirAll(filepath.Dir(replacement), 0o755); err != nil {
		t.Fatalf(mkdirAllErrorFormat, err)
	}
	if err := os.WriteFile(replacement, []byte("new"), 0o755); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
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
		t.Fatalf(readFileErrorFormat, err)
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

	renamePath = func(string, string) error { return fmt.Errorf(renameFailedError) }

	root := t.TempDir()
	current := filepath.Join(root, "Continuum")
	replacement := filepath.Join(root, "replacement", "Continuum")

	if err := os.WriteFile(current, []byte("old"), 0o755); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
	}
	if err := os.MkdirAll(filepath.Dir(replacement), 0o755); err != nil {
		t.Fatalf(mkdirAllErrorFormat, err)
	}
	if err := os.WriteFile(replacement, []byte("new"), 0o755); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
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
		t.Fatalf(writeFileErrorFormat, err)
	}
	if err := os.MkdirAll(filepath.Dir(replacement), 0o755); err != nil {
		t.Fatalf(mkdirAllErrorFormat, err)
	}
	if err := os.WriteFile(replacement, []byte("new"), 0o755); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
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
		t.Fatalf(mkdirAllErrorFormat, err)
	}
	if err := os.WriteFile(currentExec, []byte("old"), 0o755); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
	}
	if err := os.MkdirAll(filepath.Dir(replacementExec), 0o755); err != nil {
		t.Fatalf(mkdirAllErrorFormat, err)
	}
	if err := os.WriteFile(replacementExec, []byte("new"), 0o755); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
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
		t.Fatalf(readFileErrorFormat, err)
	}

	if string(data) != "new" {
		t.Fatalf("replaceAppBundle() contents = %q, want %q", string(data), "new")
	}

	if _, err := os.Stat(currentBundle + ".bak"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replaceAppBundle() left a .bak bundle behind: %v", err)
	}

	if _, err := os.Stat(bundleTempPath(currentBundle, ".incoming")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replaceAppBundle() left incoming bundle behind: %v", err)
	}

	if _, err := os.Stat(bundleTempPath(currentBundle, ".previous")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replaceAppBundle() left previous bundle behind: %v", err)
	}
}

func TestReplaceAppBundleFailure(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	renameCalls := 0
	renamePath = func(oldPath, newPath string) error {
		renameCalls++
		if renameCalls == 2 {
			return fmt.Errorf(renameFailedError)
		}
		return os.Rename(oldPath, newPath)
	}

	root := t.TempDir()
	currentBundle := filepath.Join(root, AppName+".app")
	currentExec := filepath.Join(currentBundle, "Contents", "MacOS", AppName)
	replacementBundle := filepath.Join(root, "download", AppName+".app")
	replacementExec := filepath.Join(replacementBundle, "Contents", "MacOS", AppName)

	if err := os.MkdirAll(filepath.Dir(currentExec), 0o755); err != nil {
		t.Fatalf(mkdirAllErrorFormat, err)
	}
	if err := os.WriteFile(currentExec, []byte("old"), 0o755); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
	}
	if err := os.MkdirAll(filepath.Dir(replacementExec), 0o755); err != nil {
		t.Fatalf(mkdirAllErrorFormat, err)
	}
	if err := os.WriteFile(replacementExec, []byte("new"), 0o755); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
	}

	if _, err := replaceAppBundle(currentExec, replacementBundle); err == nil {
		t.Fatal("replaceAppBundle() error = nil, want rename failure")
	}
}

func TestReplaceAppBundleInitialRenameFailure(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	root := t.TempDir()
	currentBundle := filepath.Join(root, AppName+".app")
	currentExec := filepath.Join(currentBundle, "Contents", "MacOS", AppName)
	replacementBundle := filepath.Join(root, "download", AppName+".app")
	replacementExec := filepath.Join(replacementBundle, "Contents", "MacOS", AppName)

	if err := os.MkdirAll(filepath.Dir(currentExec), 0o755); err != nil {
		t.Fatalf(mkdirAllErrorFormat, err)
	}
	if err := os.WriteFile(currentExec, []byte("old"), 0o755); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
	}
	if err := os.MkdirAll(filepath.Dir(replacementExec), 0o755); err != nil {
		t.Fatalf(mkdirAllErrorFormat, err)
	}
	if err := os.WriteFile(replacementExec, []byte("new"), 0o755); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
	}

	renamePath = func(string, string) error {
		return fmt.Errorf(renameFailedError)
	}

	if _, err := replaceAppBundle(currentExec, replacementBundle); err == nil {
		t.Fatal("replaceAppBundle() error = nil, want initial rename failure")
	}

	stagedBundle := bundleTempPath(currentBundle, ".incoming")
	if _, err := os.Stat(stagedBundle); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replaceAppBundle() left staged bundle behind: %v", err)
	}
}

func TestReplaceAppBundleNormalizesHiddenCurrentBundle(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	currentBundle := filepath.Join(root, "."+AppName+".app")
	currentExec := filepath.Join(currentBundle, "Contents", "MacOS", AppName)
	replacementBundle := filepath.Join(root, "download", AppName+".app")
	replacementExec := filepath.Join(replacementBundle, "Contents", "MacOS", AppName)
	visibleBundle := filepath.Join(root, AppName+".app")
	visibleExec := filepath.Join(visibleBundle, "Contents", "MacOS", AppName)

	if err := os.MkdirAll(filepath.Dir(currentExec), 0o755); err != nil {
		t.Fatalf(mkdirAllErrorFormat, err)
	}
	if err := os.WriteFile(currentExec, []byte("old"), 0o755); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
	}
	if err := os.MkdirAll(filepath.Dir(replacementExec), 0o755); err != nil {
		t.Fatalf(mkdirAllErrorFormat, err)
	}
	if err := os.WriteFile(replacementExec, []byte("new"), 0o755); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
	}

	got, err := replaceAppBundle(currentExec, replacementBundle)
	if err != nil {
		t.Fatalf("replaceAppBundle() error = %v", err)
	}

	if got != visibleExec {
		t.Fatalf("replaceAppBundle() = %q, want %q", got, visibleExec)
	}

	if _, err := os.Stat(visibleExec); err != nil {
		t.Fatalf("Stat(%q) error = %v", visibleExec, err)
	}
	if _, err := os.Stat(currentBundle); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replaceAppBundle() left hidden current bundle behind: %v", err)
	}
}

func TestReplaceAppBundleCopyTreeError(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	currentExec := filepath.Join(root, AppName+".app", "Contents", "MacOS", AppName)
	if err := os.MkdirAll(filepath.Dir(currentExec), 0o755); err != nil {
		t.Fatalf(mkdirAllErrorFormat, err)
	}
	if err := os.WriteFile(currentExec, []byte("old"), 0o755); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
	}

	if _, err := replaceAppBundle(currentExec, filepath.Join(root, "missing", AppName+".app")); err == nil {
		t.Fatal("replaceAppBundle() error = nil, want copy-tree failure")
	}
}

func TestReplaceAppBundleInvalidExecutable(t *testing.T) {
	t.Parallel()

	if _, err := replaceAppBundle(tmpContinuumBinary, "/tmp/download/Continuum.app"); err == nil {
		t.Fatal("replaceAppBundle() error = nil, want invalid bundle failure")
	}
}

func TestCopyFileSourceOpenError(t *testing.T) {
	t.Parallel()

	if err := copyFile(filepath.Join(t.TempDir(), "missing"), filepath.Join(t.TempDir(), "dest", AppName)); err == nil {
		t.Fatal("copyFile() error = nil, want source open failure")
	}
}

func TestCopyFileCreateDirsError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	root := t.TempDir()
	source := filepath.Join(root, "source", AppName)
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatalf(mkdirAllErrorFormat, err)
	}
	if err := os.WriteFile(source, []byte("data"), 0o644); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
	}

	createDirs = func(string, os.FileMode) error {
		return fmt.Errorf("mkdir failed")
	}

	if err := copyFile(source, filepath.Join(root, "dest", AppName)); err == nil {
		t.Fatal("copyFile() error = nil, want destination directory failure")
	}
}

func TestCopyFileStatError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	root := t.TempDir()
	source := filepath.Join(root, "source", AppName)
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatalf(mkdirAllErrorFormat, err)
	}
	if err := os.WriteFile(source, []byte("data"), 0o644); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
	}

	statOpenFile = func(*os.File) (os.FileInfo, error) {
		return nil, fmt.Errorf("stat failed")
	}

	if err := copyFile(source, filepath.Join(root, "dest", AppName)); err == nil {
		t.Fatal("copyFile() error = nil, want stat failure")
	}
}

func TestCopyFileDestinationOpenError(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := filepath.Join(root, "source", AppName)
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatalf(mkdirAllErrorFormat, err)
	}
	if err := os.WriteFile(source, []byte("data"), 0o644); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
	}

	destination := filepath.Join(root, "destdir")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatalf(mkdirAllErrorFormat, err)
	}

	if err := copyFile(source, destination); err == nil {
		t.Fatal("copyFile() error = nil, want destination open failure")
	}
}

func TestCopyFileCopyError(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := filepath.Join(root, "source-dir")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatalf(mkdirAllErrorFormat, err)
	}

	if err := copyFile(source, filepath.Join(root, "dest", AppName)); err == nil {
		t.Fatal("copyFile() error = nil, want copy failure")
	}
}

func TestCopyTreeMissingSource(t *testing.T) {
	t.Parallel()

	if err := copyTree(filepath.Join(t.TempDir(), "missing"), filepath.Join(t.TempDir(), "dest")); err == nil {
		t.Fatal("copyTree() error = nil, want missing source failure")
	}
}

func TestCopyTreeCreateDirsError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	root := t.TempDir()
	sourceRoot := filepath.Join(root, "source")
	if err := os.MkdirAll(filepath.Join(sourceRoot, "nested"), 0o755); err != nil {
		t.Fatalf(mkdirAllErrorFormat, err)
	}

	createDirs = func(string, os.FileMode) error {
		return fmt.Errorf("mkdir failed")
	}

	if err := copyTree(sourceRoot, filepath.Join(root, "dest")); err == nil {
		t.Fatal("copyTree() error = nil, want directory creation failure")
	}
}

func TestCopyTreeRelativePathError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	root := t.TempDir()
	sourceRoot := filepath.Join(root, "source")
	if err := os.MkdirAll(sourceRoot, 0o755); err != nil {
		t.Fatalf(mkdirAllErrorFormat, err)
	}

	relativePath = func(string, string) (string, error) {
		return "", fmt.Errorf("rel failed")
	}

	if err := copyTree(sourceRoot, filepath.Join(root, "dest")); err == nil {
		t.Fatal("copyTree() error = nil, want relative path failure")
	}
}

func TestCopyTreeDirInfoError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	walkDirectory = func(root string, fn fs.WalkDirFunc) error {
		return fn(root, fakeDirEntry{name: "dir", dir: true, infoErr: fmt.Errorf("info failed")}, nil)
	}

	if err := copyTree(t.TempDir(), filepath.Join(t.TempDir(), "dest")); err == nil {
		t.Fatal("copyTree() error = nil, want dir info failure")
	}
}

func TestWindowsUpdateScript(t *testing.T) {
	t.Parallel()

	script := windowsUpdateScript(`C:\Apps\`+windowsBinaryName, `C:\Temp\`+windowsBinaryName)
	if !strings.Contains(script, `set "CURRENT=C:\Apps\`+windowsBinaryName+`"`) {
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
	current := filepath.Join(tempDir, windowsBinaryName)
	replacement := filepath.Join(tempDir, "download", windowsBinaryName)

	if err := os.MkdirAll(filepath.Dir(replacement), 0o755); err != nil {
		t.Fatalf(mkdirAllErrorFormat, err)
	}

	called := false
	startOSProcess = func(name string, argv []string, attr *os.ProcAttr) (*os.Process, error) {
		called = true

		if name != "cmd" {
			t.Fatalf(processMismatchFormat, name, "cmd")
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

	if err := scheduleWindowsReplacement(`C:\Apps\`+windowsBinaryName, `C:\Temp\`+windowsBinaryName); err == nil {
		t.Fatal("scheduleWindowsReplacement() error = nil, want write failure")
	}
}

func TestScheduleWindowsReplacementStartError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	startOSProcess = func(string, []string, *os.ProcAttr) (*os.Process, error) {
		return nil, fmt.Errorf("start failed")
	}

	if err := scheduleWindowsReplacement(`C:\Apps\`+windowsBinaryName, `C:\Temp\`+windowsBinaryName); err == nil {
		t.Fatal("scheduleWindowsReplacement() error = nil, want start failure")
	}
}

func TestRelaunchBinary(t *testing.T) {
	restore := stubUpdaterHooks(t)

	called := false
	startOSProcess = func(name string, argv []string, attr *os.ProcAttr) (*os.Process, error) {
		called = true

		if name != tmpContinuumBinary {
			t.Fatalf(processMismatchFormat, name, tmpContinuumBinary)
		}
		if attr == nil || attr.Dir != "/tmp" {
			t.Fatalf("Dir = %q, want %q", attr.Dir, "/tmp")
		}

		return os.FindProcess(os.Getpid())
	}

	if err := relaunchBinary(tmpContinuumBinary); err != nil {
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

	if err := relaunchBinary(tmpContinuumBinary); err == nil {
		t.Fatal("relaunchBinary() error = nil, want start failure")
	}
}

func TestRelaunchBinaryUsesOpenForAppBundle(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	appBinary := filepath.Join("/Applications", AppName+".app", "Contents", "MacOS", AppName)
	called := false
	startOSProcess = func(name string, argv []string, attr *os.ProcAttr) (*os.Process, error) {
		called = true

		if name != "open" {
			t.Fatalf(processMismatchFormat, name, "open")
		}
		if len(argv) != 3 || argv[0] != "open" || argv[1] != "-n" || argv[2] != filepath.Join("/Applications", AppName+".app") {
			t.Fatalf("argv = %q, want open -n %q", argv, filepath.Join("/Applications", AppName+".app"))
		}
		if attr == nil || attr.Dir != "/Applications" {
			t.Fatalf("Dir = %q, want %q", attr.Dir, "/Applications")
		}

		return os.FindProcess(os.Getpid())
	}

	if err := relaunchBinary(appBinary); err != nil {
		t.Fatalf("relaunchBinary() error = %v", err)
	}

	if !called {
		t.Fatal("relaunchBinary() did not use open for app bundle relaunch")
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
	runCheckStatus = func() Status {
		calls++
		return Status{}
	}
	newLoopTicker = func(time.Duration) loopTicker {
		return fakeTicker{channel: tickChannel}
	}

	StartBackground()

	if calls != 1 {
		t.Fatalf("StartBackground() runCheckStatus calls = %d, want %d", calls, 1)
	}
}

func TestStartBackgroundPublishesStatus(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	startOnce = sync.Once{}
	tickChannel := make(chan time.Time)
	close(tickChannel)
	want := Status{
		CurrentVersion: "1.5.0",
		RemoteVersion:  "v1.6.0",
		UpdateRequired: true,
	}
	var got Status
	published := 0

	startAsync = func(fn func()) { fn() }
	runCheckStatus = func() Status { return want }
	SetStatusObserver(func(status Status) {
		got = status
		published++
	})
	newLoopTicker = func(time.Duration) loopTicker {
		return fakeTicker{channel: tickChannel}
	}

	StartBackground()

	if published != 1 {
		t.Fatalf("StartBackground() published = %d, want %d", published, 1)
	}
	if got != want {
		t.Fatalf("StartBackground() status = %#v, want %#v", got, want)
	}
}

func TestCheckAndApplyWrapper(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	currentVersion = func() string { return "dev" }
	if err := CheckAndApply(); err != nil {
		t.Fatalf("CheckAndApply() error = %v", err)
	}

	if got := cachedUpdateError(); got != "" {
		t.Fatalf("cachedUpdateError() = %q, want empty string", got)
	}
}

func TestCheckStatusWrapper(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, releaseJSON)
	}))
	defer server.Close()

	apiBaseURL = server.URL
	httpClient = server.Client()
	currentVersion = func() string { return "1.5.0" }

	got := CheckStatus()
	assertCheckStatus(t, got, "1.5.0", stableReleaseTag, true, "")
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
	assertCheckStatus(t, got, "1.5.0", stableReleaseTag, true, "")
}

func TestCheckStatusSkipsUpdateWhenVersionsMatch(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, singleReleaseJSON(stableReleaseTagV200))
	}))
	defer server.Close()

	apiBaseURL = server.URL
	httpClient = server.Client()

	got := checkStatus(context.Background(), "2.0.0")
	assertCheckStatus(t, got, "2.0.0", stableReleaseTagV200, false, "")
}

func TestCheckStatusSkipsUpdateWhenCurrentIsNewer(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, singleReleaseJSON(stableReleaseTagV200))
	}))
	defer server.Close()

	apiBaseURL = server.URL
	httpClient = server.Client()

	got := checkStatus(context.Background(), "2.0.1")
	assertCheckStatus(t, got, "2.0.1", stableReleaseTagV200, false, "")
}

func TestCheckStatusHandlesUnavailableRemote(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	apiBaseURL = ":"

	got := checkStatus(context.Background(), "1.5.0")
	assertCheckStatus(t, got, "1.5.0", unavailableRemote, false, "")
}

func TestCheckStatusSkipsInvalidCurrentVersion(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	got := checkStatus(context.Background(), "dev")
	assertCheckStatus(t, got, "dev", unavailableRemote, false, "")
}

func TestCheckStatusHandlesLatestStableReleaseError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"tag_name":"latest","prerelease":false,"assets":[]}]`)
	}))
	defer server.Close()

	apiBaseURL = server.URL
	httpClient = server.Client()

	got := checkStatus(context.Background(), "1.5.0")
	assertCheckStatus(t, got, "1.5.0", unavailableRemote, false, "")
}

func TestCheckStatusIncludesCachedUpdateError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	storeUpdateError(errors.New(updateFailureText))
	apiBaseURL = ":"

	got := checkStatus(context.Background(), "1.5.0")
	assertCheckStatus(t, got, "1.5.0", unavailableRemote, false, updateFailureText)
}

func TestCheckAndApplyWrapperStoresUpdateError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	currentVersion = func() string { return "1.5.0" }
	apiBaseURL = ":"

	err := CheckAndApply()
	if err == nil {
		t.Fatal("CheckAndApply() error = nil, want fetch failure")
	}
	if got := cachedUpdateError(); got == "" {
		t.Fatal("cachedUpdateError() = empty string, want stored failure")
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

	if got := RemoteVersion(); got != stableReleaseTag {
		t.Fatalf("RemoteVersion() = %q, want %q", got, stableReleaseTag)
	}

	apiBaseURL = ":"

	if got := RemoteVersion(); got != stableReleaseTag {
		t.Fatalf("RemoteVersion() cached = %q, want %q", got, stableReleaseTag)
	}
}

func TestRemoteVersionUnavailableOnFetchError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	apiBaseURL = ":"

	if got := RemoteVersion(); got != unavailableRemote {
		t.Fatalf("RemoteVersion() = %q, want %q", got, unavailableRemote)
	}
}

func TestFetchLatestRemoteVersionLatestStableReleaseError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"tag_name":"latest","prerelease":false,"assets":[]}]`)
	}))
	defer server.Close()

	apiBaseURL = server.URL
	httpClient = server.Client()

	if _, err := fetchLatestRemoteVersion(context.Background()); err == nil {
		t.Fatal("fetchLatestRemoteVersion() error = nil, want latest-release failure")
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
	runCheckStatus = func() Status {
		calls++
		return Status{}
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

func TestRunLoopPublishesStatusOnEachTick(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	startOnce = sync.Once{}
	tickChannel := make(chan time.Time, 1)
	tickChannel <- time.Now()
	close(tickChannel)

	statuses := []Status{
		{CurrentVersion: "1.0.0", RemoteVersion: "v1.0.0"},
		{CurrentVersion: "1.0.0", RemoteVersion: "v1.1.0", UpdateRequired: true},
	}
	statusIndex := 0
	published := make([]Status, 0, 2)

	runCheckStatus = func() Status {
		status := statuses[statusIndex]
		if statusIndex < len(statuses)-1 {
			statusIndex++
		}
		return status
	}
	SetStatusObserver(func(status Status) {
		published = append(published, status)
	})
	newLoopTicker = func(time.Duration) loopTicker {
		return fakeTicker{channel: tickChannel}
	}

	runLoop(DefaultCheckInterval)

	if len(published) != 2 {
		t.Fatalf("runLoop() published = %d, want %d", len(published), 2)
	}
	if published[0] != statuses[0] || published[1] != statuses[1] {
		t.Fatalf("runLoop() published = %#v, want %#v", published, statuses)
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
		t.Fatalf(writeFileErrorFormat, err)
	}

	assetName := buildAssetName(stableReleaseTag, "linux", "amd64")
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
			t.Fatalf(processMismatchFormat, name, current)
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
		t.Fatalf(readFileErrorFormat, err)
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

func TestResolveUpdateAssetFetchError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	apiBaseURL = ":"

	_, _, _, err := resolveUpdateAsset(context.Background(), version.Value{Major: 1, Minor: 5, Patch: 0}, "linux", "amd64")
	if err == nil {
		t.Fatal("resolveUpdateAsset() error = nil, want fetch failure")
	}
}

func TestResolveUpdateAssetNoStableRelease(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"tag_name":"v2.0.0-beta.1","prerelease":true,"assets":[]}]`)
	}))
	defer server.Close()

	apiBaseURL = server.URL
	httpClient = server.Client()

	assetName, assetURL, shouldUpdate, err := resolveUpdateAsset(context.Background(), version.Value{Major: 1, Minor: 5, Patch: 0}, "linux", "amd64")
	if err != nil {
		t.Fatalf("resolveUpdateAsset() error = %v", err)
	}
	if assetName != "" || assetURL != "" || shouldUpdate {
		t.Fatalf("resolveUpdateAsset() = (%q, %q, %t), want empty no-update result", assetName, assetURL, shouldUpdate)
	}
}

func TestResolveUpdateAssetNoUpdateWhenCurrentMatchesLatest(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, singleReleaseJSON(stableReleaseTag))
	}))
	defer server.Close()

	apiBaseURL = server.URL
	httpClient = server.Client()

	assetName, assetURL, shouldUpdate, err := resolveUpdateAsset(context.Background(), version.Value{Major: 1, Minor: 6, Patch: 0}, "linux", "amd64")
	if err != nil {
		t.Fatalf("resolveUpdateAsset() error = %v", err)
	}
	if assetName != "" || assetURL != "" || shouldUpdate {
		t.Fatalf("resolveUpdateAsset() = (%q, %q, %t), want empty no-update result", assetName, assetURL, shouldUpdate)
	}
}

func TestResolveUpdateAssetDarwinAcceptsMacOSAssetName(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `[{"tag_name":%q,"prerelease":false,"assets":[{"name":%q,"browser_download_url":%q}]}]`,
			stableReleaseTag,
			macosARM64AssetV160,
			linuxDownloadURL,
		)
	}))
	defer server.Close()

	apiBaseURL = server.URL
	httpClient = server.Client()

	assetName, assetURL, shouldUpdate, err := resolveUpdateAsset(context.Background(), version.Value{Major: 1, Minor: 5, Patch: 0}, "darwin", "arm64")
	if err != nil {
		t.Fatalf("resolveUpdateAsset() error = %v", err)
	}
	if !shouldUpdate {
		t.Fatal("resolveUpdateAsset() shouldUpdate = false, want true")
	}
	if assetName != macosARM64AssetV160 {
		t.Fatalf("resolveUpdateAsset() assetName = %q, want %q", assetName, macosARM64AssetV160)
	}
	if assetURL != linuxDownloadURL {
		t.Fatalf("resolveUpdateAsset() assetURL = %q, want %q", assetURL, linuxDownloadURL)
	}
}

func TestResolveUpdateAssetMissingAsset(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, singleReleaseJSON(stableReleaseTag))
	}))
	defer server.Close()

	apiBaseURL = server.URL
	httpClient = server.Client()

	if _, _, _, err := resolveUpdateAsset(context.Background(), version.Value{Major: 1, Minor: 5, Patch: 0}, "linux", "amd64"); err == nil {
		t.Fatal("resolveUpdateAsset() error = nil, want missing asset failure")
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

func TestCheckAndApplyPropagatesResolveUpdateAssetError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"tag_name":"v1.6.0","prerelease":false,"assets":[{"name":"latest","browser_download_url":"https://example.test/latest"}]}]`)
	}))
	defer server.Close()

	apiBaseURL = server.URL
	httpClient = server.Client()

	if err := checkAndApply(context.Background(), "1.5.0", "linux", "arm64"); err == nil {
		t.Fatal("checkAndApply() error = nil, want resolve-update failure")
	}
}

func TestCheckAndApplyMissingAsset(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, singleReleaseJSON(stableReleaseTag))
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
		case releasesAPIPath:
			fmt.Fprintf(w, `[{"tag_name":%q,"prerelease":false,"assets":[{"name":%q,"browser_download_url":%q}]}]`,
				stableReleaseTag,
				buildAssetName(stableReleaseTag, "linux", "amd64"),
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

func TestCheckAndApplyCreateTempDirError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	resolveAsset = func(context.Context, version.Value, string, string) (string, string, bool, error) {
		return assetArchiveName, linuxDownloadURL, true, nil
	}
	createTempDir = func(string, string) (string, error) {
		return "", fmt.Errorf("mktemp failed")
	}

	if err := checkAndApply(context.Background(), "1.5.0", "linux", "amd64"); err == nil {
		t.Fatal("checkAndApply() error = nil, want tempdir failure")
	}
}

func TestCheckAndApplyCreateExtractDirError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	workDir := t.TempDir()
	resolveAsset = func(context.Context, version.Value, string, string) (string, string, bool, error) {
		return assetArchiveName, linuxDownloadURL, true, nil
	}
	createTempDir = func(string, string) (string, error) { return workDir, nil }
	downloadAsset = func(context.Context, *http.Client, string, string) error { return nil }
	createDirs = func(path string, mode os.FileMode) error {
		if path == filepath.Join(workDir, "extract") {
			return fmt.Errorf("mkdir failed")
		}
		return os.MkdirAll(path, mode)
	}

	if err := checkAndApply(context.Background(), "1.5.0", "linux", "amd64"); err == nil {
		t.Fatal("checkAndApply() error = nil, want extract-dir failure")
	}
}

func TestCheckAndApplyExtractArchiveError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	workDir := t.TempDir()
	resolveAsset = func(context.Context, version.Value, string, string) (string, string, bool, error) {
		return assetArchiveName, linuxDownloadURL, true, nil
	}
	createTempDir = func(string, string) (string, error) { return workDir, nil }
	downloadAsset = func(context.Context, *http.Client, string, string) error { return nil }
	extractArchive = func(string, string) error { return fmt.Errorf("extract failed") }

	if err := checkAndApply(context.Background(), "1.5.0", "linux", "amd64"); err == nil {
		t.Fatal("checkAndApply() error = nil, want archive extraction failure")
	}
}

func TestCheckAndApplyApplyUpdateError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	workDir := t.TempDir()
	resolveAsset = func(context.Context, version.Value, string, string) (string, string, bool, error) {
		return assetArchiveName, linuxDownloadURL, true, nil
	}
	createTempDir = func(string, string) (string, error) { return workDir, nil }
	downloadAsset = func(context.Context, *http.Client, string, string) error { return nil }
	extractArchive = func(string, string) error { return nil }
	applyUpdate = func(string, string) (string, bool, error) {
		return "", false, fmt.Errorf("apply failed")
	}

	if err := checkAndApply(context.Background(), "1.5.0", "linux", "amd64"); err == nil {
		t.Fatal("checkAndApply() error = nil, want update-apply failure")
	}
}

func TestCheckAndApplyRelaunchError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	workDir := t.TempDir()
	resolveAsset = func(context.Context, version.Value, string, string) (string, string, bool, error) {
		return assetArchiveName, linuxDownloadURL, true, nil
	}
	createTempDir = func(string, string) (string, error) { return workDir, nil }
	downloadAsset = func(context.Context, *http.Client, string, string) error { return nil }
	extractArchive = func(string, string) error { return nil }
	applyUpdate = func(string, string) (string, bool, error) {
		return tmpContinuumBinary, false, nil
	}
	relaunchUpdated = func(string) error { return fmt.Errorf("relaunch failed") }

	if err := checkAndApply(context.Background(), "1.5.0", "linux", "amd64"); err == nil {
		t.Fatal("checkAndApply() error = nil, want relaunch failure")
	}
}

func TestCheckAndApplyWindows(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	root := t.TempDir()
	current := filepath.Join(root, windowsBinaryName)
	if err := os.WriteFile(current, []byte("old"), 0o755); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
	}

	assetName := buildAssetName(stableReleaseTag, "windows", "amd64")
	server := releaseServer(t, assetName, map[string]string{
		windowsBinaryName: "new",
	})
	defer server.Close()

	apiBaseURL = server.URL
	httpClient = server.Client()
	currentExecutable = func() (string, error) { return current, nil }

	started := false
	startOSProcess = func(name string, argv []string, attr *os.ProcAttr) (*os.Process, error) {
		started = true
		if name != "cmd" {
			t.Fatalf(processMismatchFormat, name, "cmd")
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
		t.Fatalf(mkdirAllErrorFormat, err)
	}
	if err := os.WriteFile(currentExec, []byte("old"), 0o755); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
	}
	if err := os.MkdirAll(filepath.Dir(replacementExec), 0o755); err != nil {
		t.Fatalf(mkdirAllErrorFormat, err)
	}
	if err := os.WriteFile(replacementExec, []byte("new"), 0o755); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
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

	currentExecutable = func() (string, error) { return tmpContinuumBinary, nil }

	if _, _, err := applyExtractedUpdate("linux", t.TempDir()); err == nil {
		t.Fatal("applyExtractedUpdate() error = nil, want missing replacement failure")
	}
}

func TestApplyExtractedUpdateMissingReplacementWindows(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	currentExecutable = func() (string, error) { return filepath.Join(tmpContinuumDir, windowsBinaryName), nil }

	if _, _, err := applyExtractedUpdate("windows", t.TempDir()); err == nil {
		t.Fatal("applyExtractedUpdate() error = nil, want missing Windows replacement failure")
	}
}

func TestApplyExtractedUpdateMissingReplacementDarwin(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	currentExecutable = func() (string, error) {
		return filepath.Join(tmpContinuumDir, AppName+".app", "Contents", "MacOS", AppName), nil
	}

	if _, _, err := applyExtractedUpdate("darwin", t.TempDir()); err == nil {
		t.Fatal("applyExtractedUpdate() error = nil, want missing app bundle replacement failure")
	}
}

func TestApplyExtractedUpdateLinux(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	root := t.TempDir()
	current := filepath.Join(root, AppName)
	replacement := filepath.Join(root, "extract", AppName)

	if err := os.WriteFile(current, []byte("old"), 0o755); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
	}
	if err := os.MkdirAll(filepath.Dir(replacement), 0o755); err != nil {
		t.Fatalf(mkdirAllErrorFormat, err)
	}
	if err := os.WriteFile(replacement, []byte("new"), 0o755); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
	}

	currentExecutable = func() (string, error) { return current, nil }

	got, windowsHandoff, err := applyExtractedUpdate("linux", filepath.Join(root, "extract"))
	if err != nil {
		t.Fatalf("applyExtractedUpdate() error = %v", err)
	}
	if windowsHandoff {
		t.Fatal("applyExtractedUpdate() windowsHandoff = true, want false")
	}
	if got != current {
		t.Fatalf("applyExtractedUpdate() = %q, want %q", got, current)
	}
}

func TestApplyExtractedUpdateWindowsScheduleError(t *testing.T) {
	restore := stubUpdaterHooks(t)
	defer restore()

	root := t.TempDir()
	current := filepath.Join(root, windowsBinaryName)
	replacement := filepath.Join(root, "extract", windowsBinaryName)

	if err := os.WriteFile(current, []byte("old"), 0o755); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
	}
	if err := os.MkdirAll(filepath.Dir(replacement), 0o755); err != nil {
		t.Fatalf(mkdirAllErrorFormat, err)
	}
	if err := os.WriteFile(replacement, []byte("new"), 0o755); err != nil {
		t.Fatalf(writeFileErrorFormat, err)
	}

	currentExecutable = func() (string, error) { return current, nil }
	startOSProcess = func(string, []string, *os.ProcAttr) (*os.Process, error) {
		return nil, fmt.Errorf("start failed")
	}

	if _, _, err := applyExtractedUpdate("windows", filepath.Join(root, "extract")); err == nil {
		t.Fatal("applyExtractedUpdate() error = nil, want Windows scheduling failure")
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
	originalRunCheckStatus := runCheckStatus
	originalNewLoopTicker := newLoopTicker
	originalResolveAsset := resolveAsset
	originalDownloadAsset := downloadAsset
	originalExtractArchive := extractArchive
	originalApplyUpdate := applyUpdate
	originalRelaunchUpdated := relaunchUpdated
	originalStatOpenFile := statOpenFile
	originalWalkDirectory := walkDirectory
	originalRelativePath := relativePath
	originalCurrentVersion := currentVersion
	originalLatestRemoteVersion := latestRemoteVersion
	originalLatestUpdateError := latestUpdateError
	originalStatusObserver := statusObserver

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
	startAsync = startAsyncDefault
	runCheckAndApply = CheckAndApply
	runCheckStatus = CheckStatus
	newLoopTicker = defaultLoopTicker
	resolveAsset = resolveUpdateAsset
	downloadAsset = downloadReleaseAsset
	extractArchive = extractTarGz
	applyUpdate = applyExtractedUpdate
	relaunchUpdated = relaunchBinary
	statOpenFile = func(file *os.File) (os.FileInfo, error) { return file.Stat() }
	walkDirectory = filepath.WalkDir
	relativePath = filepath.Rel
	startOnce = sync.Once{}
	storeRemoteVersion("")
	clearUpdateError()

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
		runCheckStatus = originalRunCheckStatus
		newLoopTicker = originalNewLoopTicker
		resolveAsset = originalResolveAsset
		downloadAsset = originalDownloadAsset
		extractArchive = originalExtractArchive
		applyUpdate = originalApplyUpdate
		relaunchUpdated = originalRelaunchUpdated
		statOpenFile = originalStatOpenFile
		walkDirectory = originalWalkDirectory
		relativePath = originalRelativePath
		startOnce = sync.Once{}
		storeRemoteVersion(originalLatestRemoteVersion)
		if originalLatestUpdateError == "" {
			clearUpdateError()
		} else {
			storeUpdateError(errors.New(originalLatestUpdateError))
		}
		SetStatusObserver(originalStatusObserver)
	}
}

type fakeTicker struct {
	channel chan time.Time
}

func (f fakeTicker) Chan() <-chan time.Time {
	return f.channel
}

func (f fakeTicker) Stop() {
	// No-op: tests control ticker shutdown by closing the channel directly.
}

type fakeDirEntry struct {
	name    string
	dir     bool
	infoErr error
}

func (f fakeDirEntry) Name() string {
	return f.name
}

func (f fakeDirEntry) IsDir() bool {
	return f.dir
}

func (f fakeDirEntry) Type() fs.FileMode {
	if f.dir {
		return fs.ModeDir
	}
	return 0
}

func (f fakeDirEntry) Info() (fs.FileInfo, error) {
	if f.infoErr != nil {
		return nil, f.infoErr
	}
	return fakeFileInfo{name: f.name, mode: 0o755 | fs.ModeDir}, nil
}

type fakeFileInfo struct {
	name string
	mode fs.FileMode
}

func (f fakeFileInfo) Name() string       { return f.name }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeFileInfo) Sys() any           { return nil }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func releaseServer(t *testing.T, assetName string, files map[string]string) *httptest.Server {
	t.Helper()

	assetPath := "/download/" + assetName
	releasePayload := fmt.Sprintf(`[
  {
    "tag_name": %q,
    "prerelease": false,
    "assets": [
      {
        "name": %q,
        "browser_download_url": %q
      }
    ]
  }
]`, stableReleaseTag, assetName, "{{BASE_URL}}"+assetPath)

	archive := filepath.Join(t.TempDir(), assetName)
	if err := writeTarGz(archive, files); err != nil {
		t.Fatalf(writeTarGzErrorFormat, err)
	}

	archiveData, err := os.ReadFile(archive)
	if err != nil {
		t.Fatalf(readFileErrorFormat, err)
	}

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case releasesAPIPath:
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

func assertCheckStatus(t *testing.T, got Status, wantCurrent string, wantRemote string, wantUpdateRequired bool, wantUpdateError string) {
	t.Helper()

	if got.CurrentVersion != wantCurrent {
		t.Fatalf(checkStatusCurrentFormat, got.CurrentVersion, wantCurrent)
	}
	if got.RemoteVersion != wantRemote {
		t.Fatalf(checkStatusRemoteFormat, got.RemoteVersion, wantRemote)
	}
	if got.UpdateError != wantUpdateError {
		t.Fatalf(checkStatusErrorFormat, got.UpdateError, wantUpdateError)
	}
	if got.UpdateRequired == wantUpdateRequired {
		return
	}
	if wantUpdateRequired {
		t.Fatal("checkStatus().UpdateRequired = false, want true")
	}
	t.Fatal(checkStatusUpdateRequiredFalse)
}

func singleReleaseJSON(tag string) string {
	return fmt.Sprintf(`[{"tag_name":%q,"prerelease":false,"assets":[]}]`, tag)
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

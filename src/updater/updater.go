// Package updater checks GitHub releases for newer Continuum builds and applies
// them in place when the current binary is running a tagged release version.
package updater

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"continuum/src/version"
)

const (
	RepoOwner            = "Exohayvan"
	RepoName             = "Continuum"
	AppName              = "Continuum"
	DefaultCheckInterval = 30 * time.Minute
)

type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type Release struct {
	TagName    string  `json:"tag_name"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

type Status struct {
	CurrentVersion string `json:"currentVersion"`
	RemoteVersion  string `json:"remoteVersion"`
	UpdateRequired bool   `json:"updateRequired"`
}

var (
	errNoStableRelease  = errors.New("no stable release available")
	errNoMatchingAsset  = errors.New("no matching release asset found")
	errPathTraversal    = errors.New("archive entry escapes extraction directory")
	errAppBundleMissing = errors.New("executable is not inside an app bundle")

	apiBaseURL        = "https://api.github.com"
	httpClient        = &http.Client{Timeout: 30 * time.Second}
	currentVersion    = version.Get
	currentExecutable = os.Executable
	createTempDir     = os.MkdirTemp
	createDirs        = os.MkdirAll
	removeAllPaths    = os.RemoveAll
	removePath        = os.Remove
	renamePath        = os.Rename
	changeMode        = os.Chmod
	writeTextFile     = os.WriteFile
	startOSProcess    = os.StartProcess
	startAsync        = func(fn func()) { go fn() }
	runCheckAndApply  = CheckAndApply
	newLoopTicker     = func(interval time.Duration) loopTicker {
		return timeTicker{Ticker: time.NewTicker(interval)}
	}

	startOnce           sync.Once
	remoteVersionMu     sync.RWMutex
	latestRemoteVersion string
)

type loopTicker interface {
	Chan() <-chan time.Time
	Stop()
}

type timeTicker struct {
	*time.Ticker
}

func (t timeTicker) Chan() <-chan time.Time {
	return t.C
}

func StartBackground() {
	startOnce.Do(func() {
		startAsync(func() {
			runLoop(DefaultCheckInterval)
		})
	})
}

func CheckAndApply() error {
	return checkAndApply(context.Background(), currentVersion(), runtime.GOOS, runtime.GOARCH)
}

func CheckStatus() Status {
	return checkStatus(context.Background(), currentVersion())
}

func RemoteVersion() string {
	if cached := cachedRemoteVersion(); cached != "" {
		return cached
	}

	latest, err := fetchLatestRemoteVersion(context.Background())
	if err != nil {
		return "unavailable"
	}

	return latest
}

func runLoop(interval time.Duration) {
	if interval <= 0 {
		interval = DefaultCheckInterval
	}

	_ = runCheckAndApply()

	ticker := newLoopTicker(interval)
	defer ticker.Stop()

	for range ticker.Chan() {
		_ = runCheckAndApply()
	}
}

func checkStatus(ctx context.Context, current string) Status {
	status := Status{
		CurrentVersion: current,
		RemoteVersion:  "unavailable",
	}

	currentValue, err := version.ParseString(current)
	if err != nil {
		return status
	}

	releases, err := fetchReleases(ctx, apiBaseURL, httpClient, RepoOwner, RepoName)
	if err != nil {
		return status
	}

	latest, shouldUpdate, err := latestStableRelease(currentValue, releases)
	if err != nil {
		return status
	}

	storeRemoteVersion(latest.TagName)
	status.RemoteVersion = cachedRemoteVersion()
	status.UpdateRequired = shouldUpdate
	return status
}

func checkAndApply(ctx context.Context, current, goos, goarch string) error {
	currentValue, err := version.ParseString(current)
	if err != nil {
		return nil
	}

	assetName, assetURL, shouldUpdate, err := resolveUpdateAsset(ctx, currentValue, goos, goarch)
	if err != nil {
		return err
	}

	if !shouldUpdate {
		return nil
	}

	workDir, err := createTempDir("", "continuum-update-*")
	if err != nil {
		return err
	}

	cleanup := true
	defer func() {
		if cleanup {
			_ = removeAllPaths(workDir)
		}
	}()

	archivePath := filepath.Join(workDir, assetName)
	if err := downloadReleaseAsset(ctx, httpClient, assetURL, archivePath); err != nil {
		return err
	}

	extractDir := filepath.Join(workDir, "extract")
	if err := createDirs(extractDir, 0o755); err != nil {
		return err
	}

	if err := extractTarGz(archivePath, extractDir); err != nil {
		return err
	}

	updatedPath, windowsHandoff, err := applyExtractedUpdate(goos, extractDir)
	if err != nil {
		return err
	}

	if windowsHandoff {
		cleanup = false
		return nil
	}

	if err := relaunchBinary(updatedPath); err != nil {
		return err
	}

	return nil
}

func resolveUpdateAsset(ctx context.Context, current version.Value, goos, goarch string) (string, string, bool, error) {
	releases, err := fetchReleases(ctx, apiBaseURL, httpClient, RepoOwner, RepoName)
	if err != nil {
		return "", "", false, err
	}

	latest, shouldUpdate, err := latestStableRelease(current, releases)
	if err != nil {
		if errors.Is(err, errNoStableRelease) {
			return "", "", false, nil
		}

		return "", "", false, err
	}

	storeRemoteVersion(latest.TagName)
	if !shouldUpdate {
		return "", "", false, nil
	}

	assetName := buildAssetName(latest.TagName, goos, goarch)
	assetURL, err := findAssetURL(latest, assetName)
	if err != nil {
		return "", "", false, err
	}

	return assetName, assetURL, true, nil
}

func fetchReleases(ctx context.Context, baseURL string, client *http.Client, owner, repo string) ([]Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/%s/releases", baseURL, owner, repo), nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "continuum-updater")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch releases: unexpected status %s", resp.Status)
	}

	var releases []Release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}

	return releases, nil
}

func latestStableRelease(current version.Value, releases []Release) (Release, bool, error) {
	var (
		latest      Release
		latestValue version.Value
		found       bool
	)

	for _, release := range releases {
		if release.Prerelease {
			continue
		}

		parsed, err := version.ParseString(release.TagName)
		if err != nil {
			continue
		}

		if !found || parsed.Compare(latestValue) > 0 {
			latest = release
			latestValue = parsed
			found = true
		}
	}

	if !found {
		return Release{}, false, errNoStableRelease
	}

	return latest, latestValue.Compare(current) > 0, nil
}

func fetchLatestRemoteVersion(ctx context.Context) (string, error) {
	releases, err := fetchReleases(ctx, apiBaseURL, httpClient, RepoOwner, RepoName)
	if err != nil {
		return "", err
	}

	latest, _, err := latestStableRelease(version.Value{}, releases)
	if err != nil {
		return "", err
	}

	storeRemoteVersion(latest.TagName)
	return cachedRemoteVersion(), nil
}

func cachedRemoteVersion() string {
	remoteVersionMu.RLock()
	defer remoteVersionMu.RUnlock()

	return latestRemoteVersion
}

func storeRemoteVersion(tag string) {
	remoteVersionMu.Lock()
	defer remoteVersionMu.Unlock()

	if tag == "" {
		latestRemoteVersion = ""
		return
	}

	if strings.HasPrefix(tag, "v") {
		latestRemoteVersion = tag
		return
	}

	latestRemoteVersion = "v" + tag
}

func buildAssetName(tag, goos, goarch string) string {
	normalizedTag := tag
	if !strings.HasPrefix(normalizedTag, "v") {
		normalizedTag = "v" + normalizedTag
	}

	return fmt.Sprintf("continuum-%s-%s-%s.tar.gz", goos, goarch, normalizedTag)
}

func findAssetURL(release Release, assetName string) (string, error) {
	for _, asset := range release.Assets {
		if strings.EqualFold(asset.Name, assetName) {
			return asset.BrowserDownloadURL, nil
		}
	}

	return "", fmt.Errorf("%w: %s", errNoMatchingAsset, assetName)
}

func downloadReleaseAsset(ctx context.Context, client *http.Client, url, destination string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", "continuum-updater")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download asset: unexpected status %s", resp.Status)
	}

	if err := createDirs(filepath.Dir(destination), 0o755); err != nil {
		return err
	}

	out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func extractTarGz(archivePath, destination string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)

	for {
		header, err := tarReader.Next()
		if isTarEOF(err) {
			return nil
		}
		if err != nil {
			return err
		}

		if err := extractTarEntry(tarReader, destination, header); err != nil {
			return err
		}
	}
}

func isTarEOF(err error) bool {
	return errors.Is(err, io.EOF)
}

func extractTarEntry(tarReader *tar.Reader, destination string, header *tar.Header) error {
	targetPath, err := archiveTarget(destination, header.Name)
	if err != nil {
		return err
	}

	switch header.Typeflag {
	case tar.TypeDir:
		return createDirs(targetPath, header.FileInfo().Mode())
	case tar.TypeReg:
		return writeTarFile(tarReader, targetPath, header.FileInfo().Mode())
	default:
		return nil
	}
}

func writeTarFile(tarReader *tar.Reader, targetPath string, mode os.FileMode) error {
	if err := createDirs(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}

	out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, tarReader); err != nil {
		out.Close()
		return err
	}

	return out.Close()
}

func archiveTarget(destination, entryName string) (string, error) {
	cleanDestination := filepath.Clean(destination)
	cleanTarget := filepath.Clean(filepath.Join(cleanDestination, entryName))

	if cleanTarget == cleanDestination {
		return cleanTarget, nil
	}

	prefix := cleanDestination + string(os.PathSeparator)
	if !strings.HasPrefix(cleanTarget, prefix) {
		return "", fmt.Errorf("%w: %s", errPathTraversal, entryName)
	}

	return cleanTarget, nil
}

func applyExtractedUpdate(goos, extractDir string) (string, bool, error) {
	currentPath, err := currentExecutable()
	if err != nil {
		return "", false, err
	}

	switch goos {
	case "windows":
		replacementPath, err := findPathByName(extractDir, filepath.Base(currentPath), false)
		if err != nil {
			return "", false, err
		}

		if err := scheduleWindowsReplacement(currentPath, replacementPath); err != nil {
			return "", false, err
		}

		return currentPath, true, nil
	case "darwin":
		replacementBundle, err := findPathByName(extractDir, AppName+".app", true)
		if err != nil {
			return "", false, err
		}

		updatedPath, err := replaceAppBundle(currentPath, replacementBundle)
		return updatedPath, false, err
	default:
		replacementPath, err := findPathByName(extractDir, filepath.Base(currentPath), false)
		if err != nil {
			return "", false, err
		}

		updatedPath, err := replaceUnixBinary(currentPath, replacementPath)
		return updatedPath, false, err
	}
}

func findPathByName(root string, name string, wantDir bool) (string, error) {
	match := ""
	stop := errors.New("stop walk")

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.Name() == name && d.IsDir() == wantDir {
			match = path
			return stop
		}

		return nil
	})
	if err != nil && !errors.Is(err, stop) {
		return "", err
	}

	if match == "" {
		return "", fmt.Errorf("unable to find %s in %s", name, root)
	}

	return match, nil
}

func appBundleRoot(executable string) (string, error) {
	marker := ".app" + string(os.PathSeparator)
	index := strings.Index(executable, marker)
	if index == -1 {
		return "", errAppBundleMissing
	}

	return executable[:index+4], nil
}

func replaceUnixBinary(currentPath, replacementPath string) (string, error) {
	stagedPath := siblingTempPath(currentPath, ".incoming")
	_ = removePath(stagedPath)

	if err := copyFile(replacementPath, stagedPath); err != nil {
		return "", err
	}

	if err := renamePath(stagedPath, currentPath); err != nil {
		_ = removePath(stagedPath)
		return "", err
	}

	if err := changeMode(currentPath, 0o755); err != nil {
		return "", err
	}

	return currentPath, nil
}

func replaceAppBundle(currentExecutablePath, replacementBundle string) (string, error) {
	currentBundle, err := appBundleRoot(currentExecutablePath)
	if err != nil {
		return "", err
	}

	stagedBundle := siblingTempPath(currentBundle, ".incoming")
	previousBundle := siblingTempPath(currentBundle, ".previous")
	_ = removeAllPaths(stagedBundle)
	_ = removeAllPaths(previousBundle)

	if err := copyTree(replacementBundle, stagedBundle); err != nil {
		return "", err
	}

	if err := renamePath(currentBundle, previousBundle); err != nil {
		_ = removeAllPaths(stagedBundle)
		return "", err
	}

	if err := renamePath(stagedBundle, currentBundle); err != nil {
		_ = renamePath(previousBundle, currentBundle)
		_ = removeAllPaths(stagedBundle)
		return "", err
	}

	_ = removeAllPaths(previousBundle)
	return filepath.Join(currentBundle, "Contents", "MacOS", filepath.Base(currentExecutablePath)), nil
}

func siblingTempPath(path, suffix string) string {
	return filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+suffix)
}

func copyFile(sourcePath, destinationPath string) error {
	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	sourceInfo, err := sourceFile.Stat()
	if err != nil {
		return err
	}

	if err := createDirs(filepath.Dir(destinationPath), 0o755); err != nil {
		return err
	}

	destinationFile, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, sourceInfo.Mode())
	if err != nil {
		return err
	}
	defer func() {
		_ = destinationFile.Close()
	}()

	if _, err := io.Copy(destinationFile, sourceFile); err != nil {
		return err
	}

	return nil
}

func copyTree(sourceRoot, destinationRoot string) error {
	return filepath.WalkDir(sourceRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relativePath, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}

		targetPath := filepath.Join(destinationRoot, relativePath)
		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}

			return createDirs(targetPath, info.Mode())
		}

		return copyFile(path, targetPath)
	})
}

func scheduleWindowsReplacement(currentPath, replacementPath string) error {
	scriptPath := filepath.Join(filepath.Dir(replacementPath), "continuum-update.cmd")
	if err := writeTextFile(scriptPath, []byte(windowsUpdateScript(currentPath, replacementPath)), 0o700); err != nil {
		return err
	}

	attr := &os.ProcAttr{
		Dir:   filepath.Dir(currentPath),
		Env:   os.Environ(),
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	}

	proc, err := startOSProcess("cmd", []string{"cmd", "/C", scriptPath}, attr)
	if err != nil {
		return err
	}

	return proc.Release()
}

func windowsUpdateScript(currentPath, replacementPath string) string {
	previousPath := currentPath + ".previous"

	lines := []string{
		"@echo off",
		"setlocal",
		fmt.Sprintf(`set "CURRENT=%s"`, currentPath),
		fmt.Sprintf(`set "REPLACEMENT=%s"`, replacementPath),
		fmt.Sprintf(`set "PREVIOUS=%s"`, previousPath),
		"for /L %%i in (1,1,30) do (",
		`  move /Y "%CURRENT%" "%PREVIOUS%" >nul 2>nul && goto replaced`,
		"  ping 127.0.0.1 -n 2 >nul",
		")",
		"exit /b 1",
		":replaced",
		`move /Y "%REPLACEMENT%" "%CURRENT%" >nul 2>nul || goto restore`,
		`start "" "%CURRENT%"`,
		`del /Q "%PREVIOUS%" >nul 2>nul`,
		`del "%~f0"`,
		"exit /b 0",
		":restore",
		`move /Y "%PREVIOUS%" "%CURRENT%" >nul 2>nul`,
		"exit /b 1",
	}

	return strings.Join(lines, "\r\n")
}

func relaunchBinary(path string) error {
	attr := &os.ProcAttr{
		Dir:   filepath.Dir(path),
		Env:   os.Environ(),
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	}

	proc, err := startOSProcess(path, os.Args, attr)
	if err != nil {
		return err
	}

	return proc.Release()
}

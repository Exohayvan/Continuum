package desktop

import (
	"context"
	"os"

	"continuum/src/datamanager"
	"continuum/src/networkmanager"
	"continuum/src/nodeid"
	"continuum/src/updater"
	"continuum/src/version"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

var (
	resolveNodeID        = nodeid.GetNodeID
	resolveDiskUsage     = datamanager.Snapshot
	resolveNetworkUsage  = networkmanager.Snapshot
	resolveVersion       = version.Get
	resolveRemoteVersion = updater.RemoteVersion
	resolveUpdateStatus  = updater.CheckStatus
	runUpdateNow         = updater.CheckAndApply
	runtimeQuit          = wailsruntime.Quit
	exitProcess          = os.Exit
	quitApplication      = func(ctx context.Context) {
		if ctx != nil {
			runtimeQuit(ctx)
			return
		}

		exitProcess(0)
	}
)

// App is the Wails application backend.
type App struct {
	quit func()
}

// NewApp creates the Wails application backend.
func NewApp() *App {
	return &App{
		quit: func() {
			quitApplication(nil)
		},
	}
}

// Startup runs when the application launches.
func (a *App) Startup(ctx context.Context) {
	a.quit = func() {
		quitApplication(ctx)
	}
}

// NodeID returns the machine's deterministic node identifier.
func (a *App) NodeID() string {
	return resolveNodeID()
}

// DiskUsage returns the current managed-data disk usage snapshot.
func (a *App) DiskUsage() (datamanager.DiskUsage, error) {
	return resolveDiskUsage()
}

// NetworkUsage returns the current managed network throughput snapshot.
func (a *App) NetworkUsage() networkmanager.Usage {
	return resolveNetworkUsage()
}

// Version returns the current application version string.
func (a *App) Version() string {
	return resolveVersion()
}

// RemoteVersion returns the latest known stable release version string.
func (a *App) RemoteVersion() string {
	return resolveRemoteVersion()
}

// UpdateStatus returns the current and remote versions plus whether an update is required.
func (a *App) UpdateStatus() updater.Status {
	return resolveUpdateStatus()
}

// UpdateNow downloads and applies the latest stable release when one is available.
func (a *App) UpdateNow() error {
	if err := runUpdateNow(); err != nil {
		return err
	}

	a.requestQuit()
	return nil
}

// Exit closes the application immediately.
func (a *App) Exit() {
	a.requestQuit()
}

func (a *App) requestQuit() {
	if a.quit == nil {
		quitApplication(nil)
		return
	}

	a.quit()
}

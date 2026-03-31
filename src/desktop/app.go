package desktop

import (
	"context"
	"os"

	"continuum/src/nodeid"
	"continuum/src/updater"
	"continuum/src/version"
)

var (
	resolveNodeID        = nodeid.GetNodeID
	resolveVersion       = version.Get
	resolveRemoteVersion = updater.RemoteVersion
	resolveUpdateStatus  = updater.CheckStatus
	runUpdateNow         = updater.CheckAndApply
	exitApplication      = os.Exit
)

// App is the Wails application backend.
type App struct{}

// NewApp creates the Wails application backend.
func NewApp() *App {
	return &App{}
}

// Startup runs when the application launches.
func (a *App) Startup(ctx context.Context) {
	_ = ctx
}

// NodeID returns the machine's deterministic node identifier.
func (a *App) NodeID() string {
	return resolveNodeID()
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
	return runUpdateNow()
}

// Exit closes the application immediately.
func (a *App) Exit() {
	exitApplication(0)
}

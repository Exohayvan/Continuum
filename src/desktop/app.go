package desktop

import (
	"context"

	"continuum/src/nodeid"
	"continuum/src/updater"
	"continuum/src/version"
)

var (
	resolveNodeID        = nodeid.GetNodeID
	resolveVersion       = version.Get
	resolveRemoteVersion = updater.RemoteVersion
	startAutoUpdate      = updater.StartBackground
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
	startAutoUpdate()
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

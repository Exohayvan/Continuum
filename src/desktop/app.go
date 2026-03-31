package desktop

import (
	"context"

	"continuum/src/nodeid"
)

var resolveNodeID = nodeid.GetNodeID

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

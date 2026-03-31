package desktop

import (
	"context"
	"testing"
)

func TestNewAppReturnsEmptyBackend(t *testing.T) {
	app := NewApp()
	if app == nil {
		t.Fatal("NewApp() = nil, want non-nil")
	}
}

func TestStartupAcceptsContext(t *testing.T) {
	originalStartAutoUpdate := startAutoUpdate
	started := false
	startAutoUpdate = func() { started = true }
	t.Cleanup(func() {
		startAutoUpdate = originalStartAutoUpdate
	})

	app := NewApp()
	ctx := context.WithValue(context.Background(), testContextKey("suite"), "continuum")

	app.Startup(ctx)

	if !started {
		t.Fatal("Startup() did not start the background updater")
	}
}

func TestNodeIDReturnsResolvedValue(t *testing.T) {
	originalResolveNodeID := resolveNodeID
	resolveNodeID = func() string { return "node-123" }
	t.Cleanup(func() {
		resolveNodeID = originalResolveNodeID
	})

	app := NewApp()
	if got := app.NodeID(); got != "node-123" {
		t.Fatalf("NodeID() = %q, want %q", got, "node-123")
	}
}

func TestVersionReturnsResolvedValue(t *testing.T) {
	originalResolveVersion := resolveVersion
	resolveVersion = func() string { return "1.5.0" }
	t.Cleanup(func() {
		resolveVersion = originalResolveVersion
	})

	app := NewApp()
	if got := app.Version(); got != "1.5.0" {
		t.Fatalf("Version() = %q, want %q", got, "1.5.0")
	}
}

func TestRemoteVersionReturnsResolvedValue(t *testing.T) {
	originalResolveRemoteVersion := resolveRemoteVersion
	resolveRemoteVersion = func() string { return "v1.6.0" }
	t.Cleanup(func() {
		resolveRemoteVersion = originalResolveRemoteVersion
	})

	app := NewApp()
	if got := app.RemoteVersion(); got != "v1.6.0" {
		t.Fatalf("RemoteVersion() = %q, want %q", got, "v1.6.0")
	}
}

type testContextKey string

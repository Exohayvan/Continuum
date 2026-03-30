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

	if app.ctx != nil {
		t.Fatalf("NewApp() ctx = %v, want nil", app.ctx)
	}
}

func TestStartupStoresContext(t *testing.T) {
	app := NewApp()
	ctx := context.WithValue(context.Background(), testContextKey("suite"), "continuum")

	app.Startup(ctx)

	if app.ctx != ctx {
		t.Fatal("Startup() did not persist the provided context")
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

type testContextKey string

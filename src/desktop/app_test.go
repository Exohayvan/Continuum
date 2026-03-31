package desktop

import (
	"context"
	"testing"

	"continuum/src/updater"
)

func TestNewAppReturnsEmptyBackend(t *testing.T) {
	app := NewApp()
	if app == nil {
		t.Fatal("NewApp() = nil, want non-nil")
	}
}

func TestStartupAcceptsContext(t *testing.T) {
	app := NewApp()
	ctx := context.WithValue(context.Background(), testContextKey("suite"), "continuum")

	app.Startup(ctx)
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

func TestUpdateStatusReturnsResolvedValue(t *testing.T) {
	originalResolveUpdateStatus := resolveUpdateStatus
	resolveUpdateStatus = func() updater.Status {
		return updater.Status{
			CurrentVersion: "1.5.0",
			RemoteVersion:  "v1.6.0",
			UpdateRequired: true,
		}
	}
	t.Cleanup(func() {
		resolveUpdateStatus = originalResolveUpdateStatus
	})

	app := NewApp()
	got := app.UpdateStatus()
	if got.CurrentVersion != "1.5.0" || got.RemoteVersion != "v1.6.0" || !got.UpdateRequired {
		t.Fatalf("UpdateStatus() = %#v, want update-required status", got)
	}
}

func TestUpdateNowRunsUpdater(t *testing.T) {
	originalRunUpdateNow := runUpdateNow
	originalQuitApplication := quitApplication
	called := false
	quitCalled := false
	runUpdateNow = func() error {
		called = true
		return nil
	}
	t.Cleanup(func() {
		runUpdateNow = originalRunUpdateNow
		quitApplication = originalQuitApplication
	})
	quitApplication = func(context.Context) {
		quitCalled = true
	}

	app := NewApp()
	if err := app.UpdateNow(); err != nil {
		t.Fatalf("UpdateNow() error = %v", err)
	}

	if !called {
		t.Fatal("UpdateNow() did not invoke updater")
	}

	if !quitCalled {
		t.Fatal("UpdateNow() did not request graceful quit")
	}
}

func TestExitCallsQuitApplication(t *testing.T) {
	originalQuitApplication := quitApplication
	quitCalled := false
	t.Cleanup(func() {
		quitApplication = originalQuitApplication
	})
	quitApplication = func(context.Context) {
		quitCalled = true
	}

	app := NewApp()
	app.Exit()

	if !quitCalled {
		t.Fatal("Exit() did not request graceful quit")
	}
}

type testContextKey string

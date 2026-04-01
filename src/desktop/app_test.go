package desktop

import (
	"context"
	"errors"
	"testing"

	"continuum/src/updater"
)

const (
	testNodeID        = "node-123"
	testVersion       = "1.5.0"
	testRemoteVersion = "v1.6.0"
	testUpdateError   = "download failed"
)

func TestNewAppReturnsEmptyBackend(t *testing.T) {
	app := NewApp()
	if app == nil {
		t.Fatal("NewApp() = nil, want non-nil")
	}
}

func TestStartupAcceptsContext(t *testing.T) {
	originalQuitApplication := quitApplication
	received := context.Context(nil)
	quitApplication = func(ctx context.Context) {
		received = ctx
	}
	t.Cleanup(func() {
		quitApplication = originalQuitApplication
	})

	app := NewApp()
	ctx := context.WithValue(context.Background(), testContextKey("suite"), "continuum")

	app.Startup(ctx)
	app.Exit()

	if received != ctx {
		t.Fatal("Startup() did not wire quit handler with the provided context")
	}
}

func TestQuitApplicationUsesWailsRuntimeWhenContextExists(t *testing.T) {
	originalRuntimeQuit := runtimeQuit
	originalExitProcess := exitProcess
	ctx := context.WithValue(context.Background(), testContextKey("suite"), "continuum")
	received := context.Context(nil)
	exitCode := -1
	runtimeQuit = func(got context.Context) {
		received = got
	}
	exitProcess = func(code int) {
		exitCode = code
	}
	t.Cleanup(func() {
		runtimeQuit = originalRuntimeQuit
		exitProcess = originalExitProcess
	})

	quitApplication(ctx)

	if received != ctx {
		t.Fatal("quitApplication() did not pass the context to the runtime quit path")
	}
	if exitCode != -1 {
		t.Fatalf("quitApplication() exit code = %d, want no exit", exitCode)
	}
}

func TestQuitApplicationFallsBackToExitProcessWithoutContext(t *testing.T) {
	originalRuntimeQuit := runtimeQuit
	originalExitProcess := exitProcess
	calledRuntimeQuit := false
	exitCode := -1
	runtimeQuit = func(context.Context) {
		calledRuntimeQuit = true
	}
	exitProcess = func(code int) {
		exitCode = code
	}
	t.Cleanup(func() {
		runtimeQuit = originalRuntimeQuit
		exitProcess = originalExitProcess
	})

	quitApplication(nil)

	if calledRuntimeQuit {
		t.Fatal("quitApplication() called runtime quit for nil context")
	}
	if exitCode != 0 {
		t.Fatalf("quitApplication() exit code = %d, want %d", exitCode, 0)
	}
}

func TestNodeIDReturnsResolvedValue(t *testing.T) {
	originalResolveNodeID := resolveNodeID
	resolveNodeID = func() string { return testNodeID }
	t.Cleanup(func() {
		resolveNodeID = originalResolveNodeID
	})

	app := NewApp()
	if got := app.NodeID(); got != testNodeID {
		t.Fatalf("NodeID() = %q, want %q", got, testNodeID)
	}
}

func TestVersionReturnsResolvedValue(t *testing.T) {
	originalResolveVersion := resolveVersion
	resolveVersion = func() string { return testVersion }
	t.Cleanup(func() {
		resolveVersion = originalResolveVersion
	})

	app := NewApp()
	if got := app.Version(); got != testVersion {
		t.Fatalf("Version() = %q, want %q", got, testVersion)
	}
}

func TestRemoteVersionReturnsResolvedValue(t *testing.T) {
	originalResolveRemoteVersion := resolveRemoteVersion
	resolveRemoteVersion = func() string { return testRemoteVersion }
	t.Cleanup(func() {
		resolveRemoteVersion = originalResolveRemoteVersion
	})

	app := NewApp()
	if got := app.RemoteVersion(); got != testRemoteVersion {
		t.Fatalf("RemoteVersion() = %q, want %q", got, testRemoteVersion)
	}
}

func TestUpdateStatusReturnsResolvedValue(t *testing.T) {
	originalResolveUpdateStatus := resolveUpdateStatus
	resolveUpdateStatus = func() updater.Status {
		return updater.Status{
			CurrentVersion: testVersion,
			RemoteVersion:  testRemoteVersion,
			UpdateRequired: true,
			UpdateError:    testUpdateError,
		}
	}
	t.Cleanup(func() {
		resolveUpdateStatus = originalResolveUpdateStatus
	})

	app := NewApp()
	got := app.UpdateStatus()
	if got.CurrentVersion != testVersion || got.RemoteVersion != testRemoteVersion || !got.UpdateRequired || got.UpdateError != testUpdateError {
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

func TestUpdateNowReturnsUpdaterError(t *testing.T) {
	originalRunUpdateNow := runUpdateNow
	originalQuitApplication := quitApplication
	wantErr := errors.New("update failed")
	quitCalled := false
	runUpdateNow = func() error {
		return wantErr
	}
	quitApplication = func(context.Context) {
		quitCalled = true
	}
	t.Cleanup(func() {
		runUpdateNow = originalRunUpdateNow
		quitApplication = originalQuitApplication
	})

	app := NewApp()
	err := app.UpdateNow()
	if !errors.Is(err, wantErr) {
		t.Fatalf("UpdateNow() error = %v, want %v", err, wantErr)
	}
	if quitCalled {
		t.Fatal("UpdateNow() requested quit when the updater failed")
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

func TestRequestQuitFallsBackWhenHandlerMissing(t *testing.T) {
	originalQuitApplication := quitApplication
	received := context.Context(nil)
	quitApplication = func(ctx context.Context) {
		received = ctx
	}
	t.Cleanup(func() {
		quitApplication = originalQuitApplication
	})

	app := &App{}
	app.requestQuit()

	if received != nil {
		t.Fatalf("requestQuit() context = %v, want nil", received)
	}
}

type testContextKey string

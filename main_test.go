package main

import (
	"bytes"
	"errors"
	"testing"

	"continuum/src/desktop"
	"github.com/wailsapp/wails/v2/pkg/options"
)

func TestBuildOptionsUsesExpectedShell(t *testing.T) {
	app := desktop.NewApp()
	opts := buildOptions(app)

	if opts.Title != "Continuum" {
		t.Fatalf("buildOptions() title = %q, want %q", opts.Title, "Continuum")
	}

	if opts.Width != 960 || opts.Height != 640 {
		t.Fatalf("buildOptions() size = %dx%d, want %dx%d", opts.Width, opts.Height, 960, 640)
	}

	if opts.MinWidth != 720 || opts.MinHeight != 520 {
		t.Fatalf("buildOptions() min size = %dx%d, want %dx%d", opts.MinWidth, opts.MinHeight, 720, 520)
	}

	if opts.AssetServer == nil {
		t.Fatal("buildOptions() AssetServer = nil, want non-nil")
	}

	if opts.OnStartup == nil {
		t.Fatal("buildOptions() OnStartup = nil, want non-nil")
	}

	if len(opts.Bind) != 1 || opts.Bind[0] != app {
		t.Fatalf("buildOptions() Bind = %v, want [%v]", opts.Bind, app)
	}
}

func TestRunAppCreatesBackendAndStartsWails(t *testing.T) {
	originalNewApplication := newApplication
	originalEnsureDataLayout := ensureDataLayout
	originalStartWails := startWails
	t.Cleanup(func() {
		newApplication = originalNewApplication
		ensureDataLayout = originalEnsureDataLayout
		startWails = originalStartWails
	})

	app := desktop.NewApp()
	newApplication = func() *desktop.App { return app }
	ensureCalled := false
	ensureDataLayout = func() (string, error) {
		ensureCalled = true
		return "/tmp/data", nil
	}

	called := false
	startWails = func(opts *options.App) error {
		called = true

		if opts.Title != "Continuum" {
			t.Fatalf("runApp() title = %q, want %q", opts.Title, "Continuum")
		}

		if len(opts.Bind) != 1 || opts.Bind[0] != app {
			t.Fatalf("runApp() Bind = %v, want [%v]", opts.Bind, app)
		}

		if opts.OnStartup == nil {
			t.Fatal("runApp() OnStartup = nil, want non-nil")
		}

		return nil
	}

	if err := runApp(); err != nil {
		t.Fatalf("runApp() error = %v", err)
	}

	if !called {
		t.Fatal("runApp() did not call startWails")
	}

	if !ensureCalled {
		t.Fatal("runApp() did not ensure the data layout")
	}
}

func TestRunAppReturnsDataLayoutError(t *testing.T) {
	originalEnsureDataLayout := ensureDataLayout
	originalNewApplication := newApplication
	originalStartWails := startWails
	t.Cleanup(func() {
		ensureDataLayout = originalEnsureDataLayout
		newApplication = originalNewApplication
		startWails = originalStartWails
	})

	wantErr := errors.New("data setup failed")
	ensureDataLayout = func() (string, error) {
		return "", wantErr
	}

	newApplication = func() *desktop.App {
		t.Fatal("runApp() created the app after data layout failed")
		return nil
	}
	startWails = func(*options.App) error {
		t.Fatal("runApp() started Wails after data layout failed")
		return nil
	}

	err := runApp()
	if !errors.Is(err, wantErr) {
		t.Fatalf("runApp() error = %v, want %v", err, wantErr)
	}
}

func TestMainWritesErrorWhenRunFails(t *testing.T) {
	originalRunApplication := runApplication
	originalStderrWriter := stderrWriter
	t.Cleanup(func() {
		runApplication = originalRunApplication
		stderrWriter = originalStderrWriter
	})

	runApplication = func() error { return errors.New("boot failed") }

	var errOut bytes.Buffer
	stderrWriter = &errOut

	main()

	if got := errOut.String(); got != "boot failed\n" {
		t.Fatalf("stderr = %q, want %q", got, "boot failed\n")
	}
}

func TestMainDoesNothingWhenRunSucceeds(t *testing.T) {
	originalRunApplication := runApplication
	originalStderrWriter := stderrWriter
	t.Cleanup(func() {
		runApplication = originalRunApplication
		stderrWriter = originalStderrWriter
	})

	runApplication = func() error { return nil }

	var errOut bytes.Buffer
	stderrWriter = &errOut

	main()

	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty output", errOut.String())
	}
}

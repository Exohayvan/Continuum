package main

import (
	"embed"
	"fmt"
	"io"
	"os"

	"continuum/src/desktop"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:src/frontend/src
var assets embed.FS

var (
	newApplication           = desktop.NewApp
	startWails               = wails.Run
	runApplication           = runApp
	stderrWriter   io.Writer = os.Stderr
)

func buildOptions(app *desktop.App) *options.App {
	return &options.App{
		Title:     "Continuum",
		Width:     960,
		Height:    640,
		MinWidth:  720,
		MinHeight: 520,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 17, G: 21, B: 30, A: 1},
		OnStartup:        app.Startup,
		Bind: []interface{}{
			app,
		},
	}
}

func runApp() error {
	app := newApplication()
	return startWails(buildOptions(app))
}

func main() {
	if err := runApplication(); err != nil {
		fmt.Fprintln(stderrWriter, err)
	}
}

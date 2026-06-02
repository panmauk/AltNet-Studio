package main

import (
	"embed"
	"flag"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

// startHidden is set when the binary is launched with --hidden, which is
// what the Windows Run-key entry passes on boot. We start the window
// invisible so users don't see a flash at login — they bring it forward
// from the shortcut (the single-instance handler shows the window).
var startHidden = flag.Bool("hidden", false, "start with window hidden (background)")

func main() {
	flag.Parse()

	app := NewApp()

	err := wails.Run(&options.App{
		Title:  "AltNet Studio",
		Width:  960,
		Height: 720,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 6, G: 10, B: 24, A: 1},
		// Closing the X hides the window instead of quitting — the
		// daemon keeps running in the background. To actually quit,
		// users go through Task Manager.
		HideWindowOnClose: true,
		// On boot-time autostart we come up invisible.
		StartHidden:   *startHidden,
		OnStartup:     app.startup,
		OnBeforeClose: app.shutdown,
		// Only one AltNet window per user. Relaunching the shortcut
		// (e.g. the user "opens" AltNet again from the Start menu while
		// it's hidden in the background) unhides and focuses the
		// existing window instead of starting a duplicate process.
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "altnet-desktop-c7f2a1b9",
			OnSecondInstanceLaunch: app.onSecondInstance,
		},
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}

// onSecondInstance fires inside the already-running app when the user
// re-launches AltNet.exe. We pull the window back into view.
func (a *App) onSecondInstance(_ options.SecondInstanceData) {
	if a.ctx == nil {
		return
	}
	wruntime.WindowShow(a.ctx)
	wruntime.WindowUnminimise(a.ctx)
	wruntime.Show(a.ctx)
}

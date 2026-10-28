// Command deck-launcher is deck on the desktop: an icon in the tray that starts deck, opens it in a window of its
// own, starts at login and quits; the window is deck's own client, the launcher has no screens.
package main

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed icon.png
var icon []byte

// tray is Deki's head in the tray: a black shape macOS colors itself, in color elsewhere.
//
//go:embed tray.png
var tray []byte

//go:embed tray-color.png
var trayColor []byte

func main() {
	hidden := flag.Bool("hidden", false, "start deck without opening its window, as at login")
	setup := flag.Bool("setup", false, "on Windows, make deck ready in WSL and quit, as the installer does")
	addr := flag.String("addr", Addr, "where deck listens")
	flag.Parse()
	runner, err := runnerHere()
	if err != nil {
		fmt.Fprintln(os.Stderr, "deck:", err)
		os.Exit(1)
	}
	if *setup {
		os.Exit(setUp(runner))
	}
	open(&Deck{Runner: runner, Addr: *addr}, !*hidden)
}

// runnerHere is how deck runs on this system.
func runnerHere() (Runner, error) {
	if runtime.GOOS == "windows" {
		return WSL{Exe: "wsl.exe", Distro: "deck"}, nil
	}
	return localDeck()
}

// prepare readies deck in WSL with the Linux build beside the launcher when it is not there yet, as after the
// restart WSL asked for; say tells each step.
func prepare(r Runner, say func(string)) error {
	preparing.Lock()
	defer preparing.Unlock()
	w, ok := r.(WSL)
	if !ok || w.Ready() {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return w.Setup(filepath.Join(filepath.Dir(exe), "deck-linux"), say)
}

// preparing keeps the start at login and a press of Open from readying WSL twice at once.
var preparing sync.Mutex

// setUp readies deck in WSL, telling each step on stdout, as the installer runs it.
func setUp(r Runner) int {
	w, ok := r.(WSL)
	if !ok {
		fmt.Println("deck: nothing to set up here")
		return 0
	}
	exe, err := os.Executable()
	if err == nil {
		err = w.Setup(filepath.Join(filepath.Dir(exe), "deck-linux"), func(s string) { fmt.Println(s) })
	}
	if errors.Is(err, errRestart) {
		fmt.Println(tr(err.Error()))
		return 3
	}
	if err != nil {
		fmt.Println(tr("deck did not start")+":", err)
		return 1
	}
	fmt.Println(tr("deck is ready in WSL."))
	return 0
}

// open runs the tray of deck: it starts deck at once and opens its window now when show, else from the menu.
func open(d *Deck, show bool) {
	app := application.New(application.Options{
		Name: "deck",
		Icon: icon,
		Mac:  application.MacOptions{ActivationPolicy: application.ActivationPolicyAccessory, ApplicationShouldTerminateAfterLastWindowClosed: false},
		OnShutdown: func() {
			_ = d.Stop()
		},
	})
	// deck asked for its update: the launcher puts it in place and starts it, or starts the old deck again.
	d.Update = func() {
		if err := selfUpdate(app.Quit); err != nil {
			application.InvokeSync(func() {
				app.Dialog.Error().SetTitle(tr("deck did not update")).SetMessage(tr(err.Error())).Show()
			})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			_ = d.Start(ctx)
		}
	}
	var window *application.WebviewWindow
	reveal := func() {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			err := prepare(d.Runner, func(string) {})
			if err == nil {
				err = d.Start(ctx)
			}
			var url string
			if err == nil {
				url, err = d.URL(ctx)
			}
			application.InvokeSync(func() {
				if err != nil {
					app.Dialog.Error().SetTitle(tr("deck did not start")).SetMessage(tr(err.Error())).Show()
					return
				}
				if window == nil {
					window = app.Window.NewWithOptions(application.WebviewWindowOptions{Title: "deck", URL: url, Width: 1280, Height: 860, MinWidth: 360, MinHeight: 480})
					window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
						window.Hide()
						e.Cancel()
					})
				}
				window.Show()
				window.Focus()
			})
		}()
	}
	menu := app.NewMenu()
	menu.Add(tr("Open deck")).OnClick(func(*application.Context) { reveal() })
	login := menu.AddCheckbox(tr("Start at login"), autostart())
	login.OnClick(func(*application.Context) {
		if err := setAutostart(login.Checked()); err != nil {
			login.SetChecked(!login.Checked())
			app.Dialog.Error().SetTitle(tr("Start at login")).SetMessage(err.Error()).Show()
		}
	})
	menu.AddSeparator()
	menu.Add(tr("Quit")).OnClick(func(*application.Context) { app.Quit() })
	mark := app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		mark.SetTemplateIcon(tray)
	} else {
		mark.SetIcon(trayColor)
	}
	mark.SetTooltip("deck")
	mark.SetMenu(menu)
	if show {
		reveal()
	} else {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if prepare(d.Runner, func(string) {}) == nil {
				_ = d.Start(ctx)
			}
		}()
	}
	if err := app.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "deck:", err)
		os.Exit(1)
	}
}

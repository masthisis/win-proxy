// NeuroRift — a fast, modern proxy cockpit for Windows powered by mihomo.
//
// main.go assembles the Wails v3 application: window, system tray,
// single-instance lock, close-to-tray behavior, service registration and
// the internal-event -> webview-event bridge.
//
// AI-DEVELOPER NOTES:
//   - All business logic lives in internal/*; this file (plus container.go
//     and service_*.go) is the only Wails-aware code.
//   - The frontend bindings are generated with `wails3 generate bindings`
//     (see docs/BUILDING.md) into frontend/bindings.
package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/neurorift/neurorift/internal/engine"
	"github.com/neurorift/neurorift/internal/nr"
	"github.com/neurorift/neurorift/internal/platform"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed assets/tray.png
var trayIcon []byte

//go:embed assets/tray-off.png
var trayIconOff []byte

// lifecycle implements the Wails service hooks for the whole container.
type lifecycle struct {
	c      *Container
	stopBG func()
	app    *application.App
}

func (l *lifecycle) ServiceStartup(ctx context.Context) error {
	l.c.ctx = ctx
	l.stopBG = l.c.StartBackground(ctx)
	forwardEvents(l.c, l.app)
	installBridges(l.app)
	return nil
}

func (l *lifecycle) ServiceShutdown() error {
	if l.stopBG != nil {
		l.stopBG()
	}
	// Restore OS proxy so the machine stays usable after we exit.
	if st, err := platform.GetSystemProxy(); err == nil && st.Enabled {
		_ = platform.SetSystemProxy(false, "", "")
	}
	_ = l.c.Engine.Stop()
	l.c.Close()
	return nil
}

func main() {
	startMinimized := hasFlag("--minimized") || hasFlag("-m")

	// From the very first statement, make every failure visible: GUI-
	// subsystem builds have no console, so log.Fatalf/os.Exit(1) vanish
	// silently ("it does not open"). See crashlog.go + startup_windows.go.
	cl := initCrashLog()
	defer cl.Close()
	defer func() {
		if r := recover(); r != nil {
			cl.writePanic(r)
			notifyError(cl, nil, fmt.Errorf("panic during startup: %v", r), true)
			os.Exit(1)
		}
	}()

	// Pre-flight the WebView2 runtime; offer a guided install when it is
	// missing instead of dying silently (older / LTSC Windows).
	if !ensureWebView2(cl) {
		return
	}

	container, err := NewContainer()
	if err != nil {
		startupFatal(cl, nil, fmt.Errorf("startup failed: %w", err))
	}

	lc := &lifecycle{c: container}

	app := application.New(application.Options{
		Name:        nr.AppName,
		Description: "A fast, modern proxy cockpit powered by mihomo",
		Icon:        trayIcon,
		// Wails system messages (incl. platform info) go to our log.
		Logger: container.Log,
		// Surface Wails errors: fatal ones as a dialog (Wails exits
		// right after), non-fatal ones once per unique message.
		ErrorHandler: func(err error) {
			var fe *application.FatalError
			notifyError(cl, container.Log, err, errors.As(err, &fe))
		},
		Services: []application.Service{
			application.NewService(lc), // startup/shutdown hooks first
			application.NewService(&AppService{c: container}),
			application.NewService(&CoreService{c: container}),
			application.NewService(&ProxyService{c: container}),
			application.NewService(&ProfileService{c: container}),
			application.NewService(&SettingsService{c: container}),
			application.NewService(&TestService{c: container}),
			application.NewService(&SysService{c: container}),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: nr.AppIdentifier,
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				application.Get().Show()
			},
		},
	})
	lc.app = app

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            nr.AppName,
		Width:            1180,
		Height:           780,
		MinWidth:         880,
		MinHeight:        560,
		URL:              "/",
		BackgroundColour: application.NewRGB(10, 12, 18),
		Hidden:           startMinimized,
		Windows: application.WindowsWindow{
			Theme: application.Dark,
		},
	})

	// Close-to-tray instead of quitting.
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		window.Hide()
		e.Cancel()
	})

	buildTray(app, container, window)

	if err := app.Run(); err != nil {
		startupFatal(cl, container.Log, fmt.Errorf("app error: %w", err))
	}
}

// hasFlag reports whether a CLI flag is present.
func hasFlag(f string) bool {
	for _, a := range os.Args[1:] {
		if a == f || strings.HasPrefix(a, f+"=") {
			return true
		}
	}
	return false
}

// forwardEvents pipes internal bus events into the webview.
func forwardEvents(c *Container, app *application.App) {
	ctx := c.ctx
	fwd := func(topic string) {
		c.Bus.Subscribe(ctx, topic, func(_ string, payload any) {
			app.Event.Emit(topic, payload)
		})
	}
	for _, topic := range []string{
		nr.EventCoreStatus, nr.EventCoreDownload, nr.EventCoreConsole,
		nr.EventProfilesList, nr.EventSubProgress, nr.EventLatencyResults,
		nr.EventSpeedProgress, nr.EventSpeedDone, nr.EventFailover,
		nr.EventTraffic, nr.EventConnections, nr.EventLogsCore,
		nr.EventSettingsChanged,
	} {
		fwd(topic)
	}
}

// installBridges wires the runtime-dependent helpers.
func installBridges(app *application.App) {
	clipboardTextFn = func() (string, bool) {
		return app.Clipboard.Text()
	}
	openPathFn = func(p string) error { return app.Browser.OpenFile(p) }
	openURLFn = func(u string) error { return app.Browser.OpenURL(u) }
	pickFileFn = func() (string, error) {
		return app.Dialog.OpenFile().
			SetTitle("Import Clash YAML").
			CanChooseFiles(true).
			CanChooseDirectories(false).
			AddFilter("Clash YAML", "*.yaml;*.yml").
			PromptForSingleSelection()
	}
	autostartOnFn = func(args []string) error {
		return app.Autostart.EnableWithOptions(application.AutostartOptions{
			Arguments: args,
		})
	}
	autostartOffFn = func() error { return app.Autostart.Disable() }
	autostartIsOnFn = func() (bool, error) { return app.Autostart.IsEnabled() }
}

// buildTray creates the system tray icon with its menu.
func buildTray(app *application.App, c *Container, window *application.WebviewWindow) {
	tray := app.SystemTray.New()
	tray.SetIcon(trayIcon)
	tray.SetTooltip(nr.AppName)

	menu := app.NewMenu()
	menu.Add("Show NeuroRift").OnClick(func(*application.Context) {
		window.Show()
		window.Focus()
	})

	menu.AddSeparator()
	proxyItem := menu.AddCheckbox("System Proxy", false)
	proxyItem.OnClick(func(*application.Context) {
		svc := &SysService{c: c}
		next := !proxyItem.Checked()
		if err := svc.SetSystemProxy(next); err == nil {
			proxyItem.SetChecked(next)
			app.Event.Emit("tray:sysproxy", next)
		}
	})

	modeMenu := menu.AddSubmenu("Mode")
	ruleItem := modeMenu.AddRadio("Rule", true)
	globalItem := modeMenu.AddRadio("Global", false)
	directItem := modeMenu.AddRadio("Direct", false)
	setMode := func(mode string) func(*application.Context) {
		return func(*application.Context) {
			if err := (&ProxyService{c: c}).SetMode(mode); err == nil {
				app.Event.Emit("tray:mode", mode)
			}
		}
	}
	ruleItem.OnClick(setMode("rule"))
	globalItem.OnClick(setMode("global"))
	directItem.OnClick(setMode("direct"))

	menu.AddSeparator()
	quitItem := menu.Add("Quit")
	quitItem.OnClick(func(*application.Context) {
		app.Quit()
	})

	tray.SetMenu(menu)
	tray.OnClick(func() {
		if window.IsVisible() {
			window.Hide()
		} else {
			window.Show()
			window.Focus()
		}
	})

	// Reflect engine state on the tray icon.
	c.Bus.Subscribe(c.ctx, nr.EventCoreStatus, func(_ string, payload any) {
		if st, ok := payload.(engine.Status); ok {
			if st.State == nr.CoreRunning {
				tray.SetIcon(trayIcon)
			} else {
				tray.SetIcon(trayIconOff)
			}
		}
	})
}

// Command HitK is a desktop Agentic Search knowledge base.
//
// The binary embeds the React frontend and exposes four Go services to it:
// App (window and workspace), Chat (turns and threads), KB (knowledge bases and
// retrieval) and Settings (vendors and model selection). Long-running work
// reports progress through a single protocol event stream rather than through
// return values, so the UI stays responsive while a turn is in flight.
package main

import (
	"embed"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/hitk/hitk/internal/config"
	"github.com/hitk/hitk/internal/protocol"
	"github.com/hitk/hitk/internal/service"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	// Registering the event type lets the binding generator emit a typed
	// listener signature for the frontend instead of `any`.
	application.RegisterEvent[protocol.Event](protocol.EventName)
}

func main() {
	paths, err := config.ResolvePaths()
	if err != nil {
		log.Fatalf("hitk: resolve data directory: %v", err)
	}
	logger := newLogger(paths.LogFile)

	cfgStore, err := config.Load(paths)
	if err != nil {
		log.Fatalf("hitk: load config: %v", err)
	}

	app := application.New(application.Options{
		Name:        "HitK",
		Description: "Agentic Search 知识库",
		Services:    nil, // populated below, once the container exists
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		Logger: logger,
	})

	// The container needs the app handle to emit events; the app needs the
	// services at construction time. Building the container first and then
	// attaching services resolves the cycle without a mutable global.
	var container *service.Container
	emit := func(ev *protocol.Event) {
		app.Event.Emit(protocol.EventName, ev)
	}

	container, err = service.NewContainer(paths, cfgStore, emit)
	if err != nil {
		log.Fatalf("hitk: %v", err)
	}
	defer container.Close()

	// Services are registered through the app's service registry so that the
	// binding generator and the runtime both see the same set.
	app.RegisterService(application.NewServiceWithOptions(
		service.NewAppService(container), application.ServiceOptions{Name: "App"}))
	app.RegisterService(application.NewServiceWithOptions(
		service.NewChatService(container), application.ServiceOptions{Name: "Chat"}))
	app.RegisterService(application.NewServiceWithOptions(
		service.NewKBService(container), application.ServiceOptions{Name: "KB"}))
	app.RegisterService(application.NewServiceWithOptions(
		service.NewSettingsService(container), application.ServiceOptions{Name: "Settings"}))
	app.RegisterService(application.NewServiceWithOptions(
		service.NewModelService(container), application.ServiceOptions{Name: "Model"}))

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "HitK",
		Width:     1280,
		Height:    820,
		MinWidth:  940,
		MinHeight: 600,
		// The shell draws its own titlebar so the chrome picks up the active
		// theme; a native frame would sit above it and never change colour.
		Frameless:        true,
		BackgroundColour: application.NewRGB(245, 246, 248),
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 38,
			Backdrop:                application.MacBackdropNormal,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		URL: "/",
	})

	if err := app.Run(); err != nil {
		log.Fatalf("hitk: %v", err)
	}
}

// newLogger writes structured logs to both the console and a rotating file in
// the data directory, which is what a user can hand over when something breaks.
func newLogger(logFile string) *slog.Logger {
	writers := []io.Writer{os.Stderr}
	if f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		writers = append(writers, f)
	}
	_ = os.MkdirAll(filepath.Dir(logFile), 0o755)

	return slog.New(slog.NewTextHandler(io.MultiWriter(writers...), &slog.HandlerOptions{
		Level: slog.LevelInfo,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				if t, ok := a.Value.Any().(time.Time); ok {
					a.Value = slog.StringValue(t.Format("2006-01-02 15:04:05.000"))
				}
			}
			return a
		},
	}))
}

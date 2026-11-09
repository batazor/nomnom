package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	// GOMEMLIMIT = 90% memory limit cgroup контейнера; явная переменная GOMEMLIMIT имеет приоритет,
	// без limit — no-op. GOMAXPROCS Go 1.25+ выводит из CPU limit сам.
	_ "github.com/KimMachineGun/automemlimit"

	"github.com/batazor/nomnom/boundaries/controlplane/di"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/config"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/lifecycle"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/telemetry"
)

func main() {
	// JSON logger процесса — до конфигурации, чтобы и ошибки загрузки шли по схеме логов;
	// уровень и resource из файла конфигурации run применяет после загрузки.
	telemetry.SetupLogging(telemetry.ProcessLogResource(di.AppServiceName), slog.LevelInfo)
	if err := run(); err != nil {
		slog.Error("controlplane stopped", telemetry.LogErrorKey, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(di.AppRole, di.AppServiceName)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	resource, err := telemetry.LogResourceFor(context.Background(), cfg.Telemetry)
	if err != nil {
		return err
	}
	telemetry.SetupLogging(resource, cfg.Logging.SlogLevel())

	app, err := di.InitializeApp(cfg)
	if err != nil {
		return fmt.Errorf("initialize app: %w", err)
	}

	return lifecycle.New(lifecycle.Config{
		ManagementAddr: fmt.Sprintf(":%d", cfg.Management.Port),
		ReadinessDelay: cfg.Shutdown.ReadinessDelay,
		DrainTimeout:   cfg.Shutdown.DrainTimeout,
	}).Run(context.Background(), app)
}

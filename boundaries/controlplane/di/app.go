// Package di — composition root процесса controlplane: Wire injector (wire.go)
// собирает слайс topology и gRPC-транспорт в App, а App владеет запуском и
// остановкой бизнес-порта и pipeline telemetry.
package di

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"

	grpcadapter "github.com/batazor/nomnom/boundaries/controlplane/infrastructure/grpc"
	controlplanev1 "github.com/batazor/nomnom/boundaries/controlplane/infrastructure/grpc/gen/nomnom/controlplane/v1"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/config"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/telemetry"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/publishroutes"
	topologydi "github.com/batazor/nomnom/boundaries/controlplane/slices/topology/di"
)

// AppServiceName — service.name процесса по умолчанию; OTEL_SERVICE_NAME переопределяет.
const AppServiceName = "controlplane"

// App — lifecycle.Worker бизнес-порта.
type App struct {
	server    *grpc.Server
	routes    publishroutes.Publisher
	addr      string
	listener  net.Listener
	telemetry *telemetry.Telemetry
}

func NewApp(cfg config.ServerConfig, server *grpc.Server, module *topologydi.Module) *App {
	return &App{server: server, routes: module.PublishRoutes, addr: fmt.Sprintf(":%d", cfg.Port)}
}

// Start поднимает telemetry pipeline, один раз приводит маршруты публичного
// gateway к реестру (ADR 41) и открывает бизнес-порт; после него процесс может
// стать ready. Маршруты не применились — процесс не стартует.
func (a *App) Start(ctx context.Context) error {
	tel, err := telemetry.Start(ctx, AppServiceName)
	if err != nil {
		return fmt.Errorf("start telemetry: %w", err)
	}
	a.telemetry = tel
	if err := a.routes.Publish(ctx); err != nil {
		return errors.Join(fmt.Errorf("publish routes at start: %w", err), a.telemetry.Shutdown(ctx))
	}
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", a.addr)
	if err != nil {
		return errors.Join(fmt.Errorf("listen %s: %w", a.addr, err), a.telemetry.Shutdown(ctx))
	}
	a.listener = listener
	slog.InfoContext(ctx, "controlplane listening", slog.String("server.address", listener.Addr().String()))
	return nil
}

// Run обслуживает входящие запросы до вызова Shutdown.
func (a *App) Run() error {
	if err := a.server.Serve(a.listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("serve grpc: %w", err)
	}
	return nil
}

// Shutdown прекращает приём новых запросов и дожидается начатых до дедлайна
// ctx; оставшиеся закрываются принудительно. Финальный export — после drain.
func (a *App) Shutdown(ctx context.Context) error {
	return errors.Join(a.stop(ctx), a.flushTelemetry(ctx))
}

func (a *App) flushTelemetry(ctx context.Context) error {
	if a.telemetry == nil {
		return nil
	}
	return a.telemetry.Shutdown(ctx)
}

func (a *App) stop(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		a.server.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		a.server.Stop()
		<-done
		return fmt.Errorf("graceful stop grpc: %w", ctx.Err())
	}
}

// NewGRPCServer — бизнес-порт nomnom.controlplane.v1. Незавершённый HTTP/2
// handshake ограничен, чтобы half-open соединение не держало GracefulStop.
func NewGRPCServer(module *topologydi.Module, subjects grpcadapter.SubjectResolver) *grpc.Server {
	server := grpc.NewServer(grpc.ConnectionTimeout(2*time.Second), grpc.StatsHandler(otelgrpc.NewServerHandler()))
	controlplanev1.RegisterControlPlaneServiceServer(server, grpcadapter.NewService(grpcadapter.Handlers{
		RegisterCell: module.RegisterCell,
		CreateMarket: module.CreateMarket,
		ListMarkets:  module.ListMarkets,
	}, subjects))
	return server
}

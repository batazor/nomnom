// Package di — composition roots процессов orders: Wire injectors (wire.go)
// собирают слайсы и инфраструктуру в App, TemporalWorker, KafkaConsumer,
// RelationshipReconciler, а те владеют запуском и остановкой своих
// ресурсов. Outbox публикует Debezium (ADR 37), процесса публикации нет.
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

	grpcadapter "nomnom/orders/infrastructure/grpc"
	ordersv1 "nomnom/orders/infrastructure/grpc/gen/nomnom/orders/v1"
	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/telemetry"
	orderdi "nomnom/orders/slices/order/di"
)

// AppServiceName — service.name API по умолчанию; OTEL_SERVICE_NAME переопределяет.
const AppServiceName = "orders"

// App — lifecycle.Worker бизнес-порта.
type App struct {
	server     *grpc.Server
	order      *orderdi.Module
	addr       string
	listener   net.Listener
	newMetrics func(ctx context.Context, serviceName string) (*telemetry.Metrics, error)
	metrics    *telemetry.Metrics
}

func NewApp(cfg config.ServerConfig, server *grpc.Server, order *orderdi.Module) *App {
	return &App{server: server, order: order, addr: fmt.Sprintf(":%d", cfg.Port), newMetrics: telemetry.Start}
}

// Start поднимает metrics pipeline и открывает бизнес-порт; после него процесс может стать ready.
func (a *App) Start(ctx context.Context) error {
	metrics, err := a.newMetrics(ctx, AppServiceName)
	if err != nil {
		return fmt.Errorf("start telemetry: %w", err)
	}
	a.metrics = metrics

	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", a.addr)
	if err != nil {
		return errors.Join(fmt.Errorf("listen %s: %w", a.addr, err), a.metrics.Shutdown(ctx))
	}
	a.listener = listener
	slog.InfoContext(ctx, "orders listening", slog.String("server.address", listener.Addr().String()))
	return nil
}

// Run обслуживает входящие запросы до вызова Shutdown.
func (a *App) Run() error {
	if err := a.server.Serve(a.listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("serve grpc: %w", err)
	}
	return nil
}

// Shutdown прекращает приём новых запросов, закрывает idle keep-alive
// соединения и дожидается начатых до дедлайна ctx; оставшиеся соединения
// закрываются принудительно. Финальный export метрик — после drain.
func (a *App) Shutdown(ctx context.Context) error {
	return errors.Join(a.stop(ctx), a.flushMetrics(ctx))
}

func (a *App) flushMetrics(ctx context.Context) error {
	if a.metrics == nil {
		return nil
	}
	return a.metrics.Shutdown(ctx)
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

func NewGRPCServer(module *orderdi.Module, subjects grpcadapter.SubjectResolver) *grpc.Server {
	// Bound incomplete HTTP/2 handshakes so a half-open TCP connection cannot
	// keep GracefulStop waiting beyond the process drain budget.
	// otelgrpc пишет в глобальный MeterProvider, который App.Start регистрирует.
	server := grpc.NewServer(grpc.ConnectionTimeout(2*time.Second), grpc.StatsHandler(otelgrpc.NewServerHandler()))
	ordersv1.RegisterOrdersServiceServer(server, grpcadapter.NewService(module, subjects))
	return server
}

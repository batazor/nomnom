// Package di — composition root процесса controlplane: Wire injector (wire.go)
// собирает слайс topology в App, а App владеет запуском и остановкой контроллера
// и pipeline telemetry.
package di

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/go-logr/logr"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/telemetry"
	topologydi "github.com/batazor/nomnom/boundaries/controlplane/slices/topology/di"
)

// AppServiceName — service.name процесса по умолчанию; OTEL_SERVICE_NAME переопределяет.
const AppServiceName = "controlplane"

// NewRESTConfig подключает процесс к Kubernetes API кластера (in-cluster
// ServiceAccount): реестр и маршруты живут там, а вне кластера процесс не стартует.
func NewRESTConfig() (*rest.Config, error) {
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("kubernetes in-cluster config: %w", err)
	}
	return restConfig, nil
}

// App — lifecycle.Worker контроллера.
type App struct {
	manager   manager.Manager
	published <-chan struct{}
	telemetry *telemetry.Telemetry
	cancel    context.CancelFunc
	stopped   chan struct{}
	err       error
}

func NewApp(module *topologydi.Module) *App {
	return &App{manager: module.Manager, published: module.Published, stopped: make(chan struct{})}
}

// Start поднимает telemetry pipeline, запускает контроллер и ждёт первой успешной
// публикации маршрутов: только после неё процесс становится ready. Контроллер
// повторяет неудачную публикацию сам, поэтому Start завершается ошибкой только при
// остановке контроллера или процесса.
func (a *App) Start(ctx context.Context) error {
	logger := logr.FromSlogHandler(slog.Default().Handler())
	ctrl.SetLogger(logger)
	klog.SetLogger(logger)

	tel, err := telemetry.Start(ctx, AppServiceName)
	if err != nil {
		return fmt.Errorf("start telemetry: %w", err)
	}
	a.telemetry = tel

	// Контроллер работает до Shutdown, а ctx отменяется уже по SIGTERM, до readiness delay.
	managerCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	a.cancel = cancel
	go func() {
		defer close(a.stopped)
		a.err = a.manager.Start(managerCtx)
	}()

	select {
	case <-a.published:
		slog.InfoContext(ctx, "controlplane published the routes")
		return nil
	case <-a.stopped:
		return errors.Join(fmt.Errorf("controller stopped before the first publication: %w", a.err), a.flushTelemetry(ctx))
	case <-ctx.Done():
		cancel()
		<-a.stopped
		return errors.Join(ctx.Err(), a.err, a.flushTelemetry(context.WithoutCancel(ctx)))
	}
}

// Run ждёт остановки контроллера.
func (a *App) Run() error {
	<-a.stopped
	if a.err != nil {
		return fmt.Errorf("run controller: %w", a.err)
	}
	return nil
}

// Shutdown останавливает контроллер и дожидается начатой публикации до дедлайна
// ctx. Финальный export — после остановки.
func (a *App) Shutdown(ctx context.Context) error {
	a.cancel()
	var stopErr error
	select {
	case <-a.stopped:
	case <-ctx.Done():
		stopErr = fmt.Errorf("stop controller: %w", ctx.Err())
	}
	return errors.Join(stopErr, a.flushTelemetry(ctx))
}

func (a *App) flushTelemetry(ctx context.Context) error {
	return a.telemetry.Shutdown(ctx)
}

package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// managementCloseTimeout ограничивает остановку технического порта: probes
// короткие, и закрытие не должно съедать бюджет terminationGracePeriodSeconds.
const managementCloseTimeout = time.Second

type Config struct {
	// ManagementAddr — адрес технического порта с /health/*.
	ManagementAddr string
	// ReadinessDelay — ожидание распространения not ready до остановки приёма работы.
	ReadinessDelay time.Duration
	// DrainTimeout — deadline на завершение начатой работы.
	DrainTimeout time.Duration
}

// Worker — единица работы процесса, которой владеет composition root:
// HTTP/gRPC server, consumer или Temporal worker.
type Worker interface {
	// Start захватывает ресурсы, например listener. Ошибка прерывает startup.
	Start(ctx context.Context) error
	// Run обслуживает работу до Shutdown и возвращает nil после штатной остановки.
	Run() error
	// Shutdown прекращает приём новой работы и дожидается начатой до дедлайна ctx.
	Shutdown(ctx context.Context) error
}

type Runtime struct {
	cfg    Config
	health Health
}

func New(cfg Config) *Runtime {
	return &Runtime{cfg: cfg}
}

func (r *Runtime) Health() *Health { return &r.health }

// Run выполняет lifecycle процесса: технический порт → Start → ready →
// SIGTERM/SIGINT или отмена ctx → not ready → readiness delay → Shutdown → exit.
// nil означает штатный shutdown; любая ошибка — non-zero exit.
func (r *Runtime) Run(ctx context.Context, worker Worker) error {
	// orders запускается как PID 1 без init, поэтому сигналы обрабатываются явно.
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, os.Interrupt)
	defer stop()

	management, err := r.serveManagement(ctx)
	if err != nil {
		return err
	}
	defer management.close()

	if err := worker.Start(ctx); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	runErr := make(chan error, 1)
	go func() { runErr <- worker.Run() }()
	r.health.markStarted()

	select {
	case err := <-runErr:
		r.health.markShuttingDown()
		return stoppedUnexpectedly(err)
	case <-ctx.Done():
	}
	// Повторный сигнал больше не перехватывается и завершает процесс сразу.
	stop()
	r.health.markShuttingDown()
	slog.Info("shutting down", slog.Duration("readiness_delay", r.cfg.ReadinessDelay), slog.Duration("drain_timeout", r.cfg.DrainTimeout))

	select {
	case err := <-runErr:
		return stoppedUnexpectedly(err)
	case <-time.After(r.cfg.ReadinessDelay):
	}

	drainCtx, cancel := context.WithTimeout(context.Background(), r.cfg.DrainTimeout)
	defer cancel()
	return errors.Join(worker.Shutdown(drainCtx), <-runErr)
}

func stoppedUnexpectedly(err error) error {
	if err == nil {
		err = errors.New("no error reported")
	}
	return fmt.Errorf("worker stopped before shutdown: %w", err)
}

type managementServer struct {
	server *http.Server
	done   chan struct{}
}

func (r *Runtime) serveManagement(ctx context.Context) (*managementServer, error) {
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", r.cfg.ManagementAddr)
	if err != nil {
		return nil, fmt.Errorf("listen management %s: %w", r.cfg.ManagementAddr, err)
	}
	slog.InfoContext(ctx, "management listening", slog.String("server.address", listener.Addr().String()))

	m := &managementServer{
		server: &http.Server{Handler: r.health.Handler(), ReadHeaderTimeout: 5 * time.Second},
		done:   make(chan struct{}),
	}
	go func() {
		defer close(m.done)
		if err := m.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("serve management failed", "error", err)
		}
	}()
	return m, nil
}

// close останавливает технический порт последним: до этого момента probes видят
// live=200 и ready=503.
func (m *managementServer) close() {
	ctx, cancel := context.WithTimeout(context.Background(), managementCloseTimeout)
	defer cancel()
	if err := m.server.Shutdown(ctx); err != nil {
		_ = m.server.Close()
	}
	<-m.done
}

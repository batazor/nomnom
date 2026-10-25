// Package di — composition root процесса orders: собирает слайсы и
// инфраструктуру в App и владеет их запуском и остановкой.
package di

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"time"

	"google.golang.org/grpc"

	grpcadapter "nomnom/orders/infrastructure/grpc"
	ordersv1 "nomnom/orders/infrastructure/grpc/gen/nomnom/orders/v1"
	"nomnom/orders/pkg/config"
	orderdi "nomnom/orders/slices/order/di"
)

// App — lifecycle.Worker бизнес-порта.
type App struct {
	server   *grpc.Server
	order    *orderdi.Module
	addr     string
	listener net.Listener
}

func NewApp(cfg config.ServerConfig, server *grpc.Server, order *orderdi.Module) *App {
	return &App{server: server, order: order, addr: fmt.Sprintf(":%d", cfg.Port)}
}

// Start открывает бизнес-порт; после него процесс может стать ready.
func (a *App) Start(ctx context.Context) error {
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", a.addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", a.addr, err)
	}
	a.listener = listener
	log.Printf("orders listening on %s", listener.Addr())
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
// закрываются принудительно.
func (a *App) Shutdown(ctx context.Context) error {
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
	server := grpc.NewServer(grpc.ConnectionTimeout(2 * time.Second))
	ordersv1.RegisterOrdersServiceServer(server, grpcadapter.NewService(module, subjects))
	return server
}

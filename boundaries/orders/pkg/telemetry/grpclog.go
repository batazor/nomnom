package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"google.golang.org/grpc/grpclog"
)

// grpcLogger направляет внутренние логи gRPC в JSON logger процесса. Как и
// logger gRPC по умолчанию, info и warning — диагностика транспорта: они
// пишутся на уровне debug (LOG__LEVEL=debug), error — на уровне error.
type grpcLogger struct{ logger *slog.Logger }

var _ grpclog.LoggerV2 = grpcLogger{}

// setGRPCLogger вызывается из SetupLogging до первого gRPC вызова: grpclog не
// допускает замену logger'а конкурентно с работой gRPC.
func setGRPCLogger(logger *slog.Logger) {
	grpclog.SetLoggerV2(grpcLogger{logger: logger.With(slog.String("logger", "grpc"))})
}

func (g grpcLogger) log(level slog.Level, msg string) {
	g.logger.Log(context.Background(), level, msg)
}

func (g grpcLogger) Info(args ...any)   { g.log(slog.LevelDebug, fmt.Sprint(args...)) }
func (g grpcLogger) Infoln(args ...any) { g.log(slog.LevelDebug, fmt.Sprint(args...)) }
func (g grpcLogger) Infof(format string, args ...any) {
	g.log(slog.LevelDebug, fmt.Sprintf(format, args...))
}
func (g grpcLogger) Warning(args ...any)   { g.log(slog.LevelDebug, fmt.Sprint(args...)) }
func (g grpcLogger) Warningln(args ...any) { g.log(slog.LevelDebug, fmt.Sprint(args...)) }
func (g grpcLogger) Warningf(format string, args ...any) {
	g.log(slog.LevelDebug, fmt.Sprintf(format, args...))
}
func (g grpcLogger) Error(args ...any)   { g.log(slog.LevelError, fmt.Sprint(args...)) }
func (g grpcLogger) Errorln(args ...any) { g.log(slog.LevelError, fmt.Sprint(args...)) }
func (g grpcLogger) Errorf(format string, args ...any) {
	g.log(slog.LevelError, fmt.Sprintf(format, args...))
}
func (g grpcLogger) Fatal(args ...any) { g.log(slog.LevelError, fmt.Sprint(args...)); os.Exit(1) }
func (g grpcLogger) Fatalln(args ...any) {
	g.log(slog.LevelError, fmt.Sprint(args...))
	os.Exit(1)
}
func (g grpcLogger) Fatalf(format string, args ...any) {
	g.log(slog.LevelError, fmt.Sprintf(format, args...))
	os.Exit(1)
}

// V сообщает gRPC, нужны ли подробные логи: только при LOG__LEVEL=debug.
func (g grpcLogger) V(level int) bool {
	return level <= 0 || g.logger.Enabled(context.Background(), slog.LevelDebug)
}

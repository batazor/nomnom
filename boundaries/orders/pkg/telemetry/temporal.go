package telemetry

import (
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/propagation"
	"go.temporal.io/sdk/client"
	temporalotel "go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/interceptor"
	temporallog "go.temporal.io/sdk/log"
)

// TemporalTracingInterceptor — OpenTelemetry interceptor Temporal SDK
// (contrib/opentelemetry, docs/observability.md#temporal). Он и client, и
// worker interceptor: worker, созданный от client, наследует его. Spans start,
// signal, query, update, workflow task и Activity создаются replay-aware; trace
// context едет в Temporal header. Baggage не передаётся (allowlist пуст), битый
// parent из header не валит workflow — начинается новый trace. Tracer берётся из
// глобального TracerProvider и начинает экспорт, когда Start его зарегистрирует.
func TemporalTracingInterceptor() (interceptor.Interceptor, error) {
	tracing, err := temporalotel.NewTracingInterceptor(temporalotel.TracerOptions{
		TextMapPropagator:       propagation.TraceContext{},
		DisableBaggage:          true,
		AllowInvalidParentSpans: true,
	})
	if err != nil {
		return nil, fmt.Errorf("temporal tracing interceptor: %w", err)
	}
	return tracing, nil
}

// TemporalLogger — logger Temporal SDK поверх JSON logger процесса.
func TemporalLogger() temporallog.Logger {
	return temporallog.NewStructuredLogger(slog.Default().With(slog.String("logger", "temporal")))
}

// TemporalClientOptions дополняет options client'а tracing interceptor'ом и
// logger'ом процесса; metrics handler задаёт вызывающий, когда pipeline метрик
// уже поднят.
func TemporalClientOptions(options client.Options) (client.Options, error) {
	tracing, err := TemporalTracingInterceptor()
	if err != nil {
		return options, err
	}
	options.Interceptors = append(options.Interceptors, tracing)
	if options.Logger == nil {
		options.Logger = TemporalLogger()
	}
	return options, nil
}

package messaging

import (
	"context"

	"github.com/ThreeDotsLabs/watermill/message"
	"go.opentelemetry.io/otel/propagation"
)

// TraceStateMetadata — W3C tracestate рядом с TraceParentMetadata.
const TraceStateMetadata = "tracestate"

// traceContext — только W3C Trace Context: baggage передаётся по allowlist,
// а он пуст (docs/observability.md#distributed-tracing).
var traceContext = propagation.TraceContext{}

// InjectTraceContext записывает traceparent и tracestate span'а из ctx в
// metadata сообщений. Outbox вызывает его при записи события: metadata
// сохраняется в envelope Forwarder'а, Kafka marshaler Watermill переносит её в
// headers, а producer span otelsarama продолжает этот контекст. Вне span
// metadata не меняется.
func InjectTraceContext(ctx context.Context, messages ...*message.Message) {
	for _, msg := range messages {
		if msg.Metadata == nil {
			msg.Metadata = message.Metadata{}
		}
		traceContext.Inject(ctx, propagation.MapCarrier(msg.Metadata))
	}
}

// ExtractTraceContext возвращает ctx с удалённым span context из metadata
// сообщения; некорректный traceparent игнорируется, и обработка начинает новый trace.
func ExtractTraceContext(ctx context.Context, msg *message.Message) context.Context {
	return traceContext.Extract(ctx, propagation.MapCarrier(msg.Metadata))
}

import { isSpanContextValid, trace } from "@opentelemetry/api";

/**
 * W3C `traceparent` of the active span for the Outbox record: the
 * server span of a gRPC call or the consumer span of a Kafka message
 * (pkg/telemetry/tracing.ts). Empty without an active, valid span.
 */
export function currentTraceMetadata(): { traceparent?: string } {
  const context = trace.getActiveSpan()?.spanContext();
  if (context === undefined || !isSpanContextValid(context)) return {};
  const flags = (context.traceFlags & 0xff).toString(16).padStart(2, "0");
  return { traceparent: `00-${context.traceId}-${context.spanId}-${flags}` };
}

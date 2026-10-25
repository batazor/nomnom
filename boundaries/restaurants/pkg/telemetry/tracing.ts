// OpenTelemetry tracing bootstrap (ADR 13 observability, docs/observability.md),
// the same contract as orders (pkg/telemetry/telemetry.go): W3C Trace Context
// and Baggage are always propagated, so an incoming `traceparent` reaches the
// Outbox metadata even without export; spans are exported over OTLP/gRPC only
// when OTEL_EXPORTER_OTLP_TRACES_ENDPOINT or OTEL_EXPORTER_OTLP_ENDPOINT is set.
import {
  type Context,
  context,
  propagation,
  ROOT_CONTEXT,
  type Span,
  SpanKind,
  SpanStatusCode,
  type TextMapGetter,
  trace,
} from "@opentelemetry/api";
import { AsyncLocalStorageContextManager } from "@opentelemetry/context-async-hooks";
import {
  CompositePropagator,
  W3CBaggagePropagator,
  W3CTraceContextPropagator,
} from "@opentelemetry/core";
import { OTLPTraceExporter } from "@opentelemetry/exporter-trace-otlp-grpc";
import { resourceFromAttributes } from "@opentelemetry/resources";
import {
  BatchSpanProcessor,
  type SpanProcessor,
  TracerProvider,
} from "@opentelemetry/sdk-trace";

export const OTLP_TRACES_ENDPOINT_ENV = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT";
const OTLP_ENDPOINT_ENV = "OTEL_EXPORTER_OTLP_ENDPOINT";

/** Tracer name of the spans this boundary starts itself. */
export const TRACER_NAME = "nomnom.restaurants";

export type Tracing = Readonly<{
  /** Spans are exported; `false` — only context propagation is installed. */
  enabled: boolean;
  shutdown(): Promise<void>;
}>;

let propagationInstalled = false;

/**
 * Registers the AsyncLocalStorage context manager and the W3C Trace Context +
 * Baggage propagator globally; idempotent.
 */
export function installPropagation(): void {
  if (propagationInstalled) return;
  propagationInstalled = true;
  context.setGlobalContextManager(
    new AsyncLocalStorageContextManager().enable(),
  );
  propagation.setGlobalPropagator(
    new CompositePropagator({
      propagators: [
        new W3CTraceContextPropagator(),
        new W3CBaggagePropagator(),
      ],
    }),
  );
}

export const tracesEndpointConfigured = (
  environment: Record<string, string | undefined>,
): boolean =>
  (environment[OTLP_TRACES_ENDPOINT_ENV]?.trim() ?? "") !== "" ||
  (environment[OTLP_ENDPOINT_ENV]?.trim() ?? "") !== "";

export function startTracing(
  serviceName: string,
  environment: Record<string, string | undefined> = process.env,
  options: { spanProcessors?: SpanProcessor[] } = {},
): Tracing {
  installPropagation();
  const spanProcessors =
    options.spanProcessors ??
    (tracesEndpointConfigured(environment)
      ? // The exporter reads endpoint, headers and protocol options from OTEL_*.
        [new BatchSpanProcessor({ exporter: new OTLPTraceExporter() })]
      : []);
  if (spanProcessors.length === 0) {
    return { enabled: false, shutdown: async () => {} };
  }
  const provider = new TracerProvider({
    resource: resourceFromAttributes({
      "service.name": environment.OTEL_SERVICE_NAME?.trim() || serviceName,
      "service.namespace": "nomnom",
    }),
    spanProcessors,
  });
  trace.setGlobalTracerProvider(provider);
  return {
    enabled: true,
    shutdown: async () => {
      await provider.shutdown();
      trace.disable();
    },
  };
}

const headersGetter: TextMapGetter<Readonly<Record<string, string>>> = {
  keys: (carrier) => Object.keys(carrier),
  get: (carrier, key) => {
    const lower = key.toLowerCase();
    for (const [name, value] of Object.entries(carrier)) {
      if (name.toLowerCase() === lower) return value;
    }
    return undefined;
  },
};

const fetchHeadersGetter: TextMapGetter<Headers> = {
  keys: (carrier) => [...carrier.keys()],
  get: (carrier, key) => carrier.get(key) ?? undefined,
};

/** Parent context from W3C headers of an incoming request or message. */
export const extractContext = (
  headers: Headers | Readonly<Record<string, string>>,
): Context =>
  headers instanceof Headers
    ? propagation.extract(ROOT_CONTEXT, headers, fetchHeadersGetter)
    : propagation.extract(ROOT_CONTEXT, headers, headersGetter);

/**
 * Runs `work` inside a new active span that continues `parent`; an error
 * marks the span and is rethrown. The span ends when `work` settles.
 */
export async function withSpan<T>(
  name: string,
  options: Readonly<{
    parent: Context;
    kind: SpanKind;
    attributes?: Record<string, string | number | boolean>;
  }>,
  work: (span: Span) => Promise<T>,
): Promise<T> {
  const tracer = trace.getTracer(TRACER_NAME);
  return tracer.startActiveSpan(
    name,
    { kind: options.kind, attributes: options.attributes ?? {} },
    options.parent,
    async (span) => {
      try {
        return await work(span);
      } catch (error) {
        span.recordException(error instanceof Error ? error : String(error));
        span.setStatus({
          code: SpanStatusCode.ERROR,
          message: error instanceof Error ? error.message : String(error),
        });
        throw error;
      } finally {
        span.end();
      }
    },
  );
}

export { SpanKind };

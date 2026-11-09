// OpenTelemetry tracing bootstrap (ADR 9, docs/observability.md), the same
// contract as orders (pkg/telemetry/telemetry.go): W3C Trace Context is always
// propagated, so an incoming `traceparent` reaches the Outbox metadata even
// without export; Baggage passes only by allowlist. Spans are exported over
// OTLP (`spec.telemetry.protocol`) through a bounded BatchSpanProcessor only
// with `spec.tracing.enabled`; OTEL_* is not read.
import {
  type Attributes,
  type BaggageEntry,
  type Context,
  context,
  type Link,
  propagation,
  ROOT_CONTEXT,
  type Span,
  SpanKind,
  SpanStatusCode,
  type TextMapGetter,
  type TextMapPropagator,
  type TextMapSetter,
  trace,
} from "@opentelemetry/api";
import { AsyncLocalStorageContextManager } from "@opentelemetry/context-async-hooks";
import {
  CompositePropagator,
  W3CBaggagePropagator,
  W3CTraceContextPropagator,
} from "@opentelemetry/core";
import { resourceFromAttributes } from "@opentelemetry/resources";
import {
  AlwaysOnSampler,
  BatchSpanProcessor,
  ParentBasedSampler,
  type SpanProcessor,
  TracerProvider,
} from "@opentelemetry/sdk-trace";

import type { OtlpExport } from "../config/telemetry.ts";
import { errorType } from "./logger.ts";
import { otlpSpanExporter } from "./otlp.ts";
import type { ResourceAttributes } from "./resource.ts";

/** Tracer name of the spans this boundary starts itself. */
export const TRACER_NAME = "nomnom.restaurants";

/**
 * Baggage keys accepted from callers and passed on. Incoming baggage is
 * untrusted and may carry personal data, so everything else is dropped;
 * a key is added here only with a concrete consumer.
 */
export const BAGGAGE_ALLOWLIST: readonly string[] = [];

/** Upper bound of one allowed baggage value. */
const BAGGAGE_VALUE_MAX = 256;

/**
 * Bounded export queue (docs/observability.md, "Эксплуатационные правила"):
 * a slow or unavailable collector drops spans instead of growing memory.
 */
export const SPAN_EXPORT_LIMITS = Object.freeze({
  maxQueueSize: 2048,
  maxExportBatchSize: 512,
  scheduledDelayMillis: 5000,
  exportTimeoutMillis: 10_000,
});

export type Tracing = Readonly<{
  /** Spans are exported; `false` — only context propagation is installed. */
  enabled: boolean;
  /** Ends export with a final flush. */
  shutdown(): Promise<void>;
}>;

/** W3C Baggage restricted to `allowlist`, both on extract and inject. */
export class AllowlistBaggagePropagator implements TextMapPropagator {
  readonly #inner = new W3CBaggagePropagator();
  readonly #allowed: ReadonlySet<string>;

  constructor(allowlist: readonly string[]) {
    this.#allowed = new Set(allowlist);
  }

  #filter(ctx: Context): Context {
    const baggage = propagation.getBaggage(ctx);
    if (baggage === undefined) return ctx;
    const entries: Record<string, BaggageEntry> = {};
    for (const [key, entry] of baggage.getAllEntries()) {
      if (this.#allowed.has(key) && entry.value.length <= BAGGAGE_VALUE_MAX) {
        entries[key] = entry;
      }
    }
    return Object.keys(entries).length === 0
      ? propagation.deleteBaggage(ctx)
      : propagation.setBaggage(ctx, propagation.createBaggage(entries));
  }

  inject(ctx: Context, carrier: unknown, setter: TextMapSetter): void {
    this.#inner.inject(this.#filter(ctx), carrier, setter);
  }

  extract(ctx: Context, carrier: unknown, getter: TextMapGetter): Context {
    return this.#filter(this.#inner.extract(ctx, carrier, getter));
  }

  fields(): string[] {
    return this.#inner.fields();
  }
}

let propagationInstalled = false;

/**
 * Registers the AsyncLocalStorage context manager and the W3C Trace Context +
 * allowlisted Baggage propagator globally; idempotent. An invalid incoming
 * `traceparent` is ignored by the propagator, so a new trace starts.
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
        new AllowlistBaggagePropagator(BAGGAGE_ALLOWLIST),
      ],
    }),
  );
}

/**
 * Installs propagation and, with `traces` (spec.tracing.enabled), exports
 * spans of `resource`. `spanProcessors` replace the OTLP exporter (tests).
 */
export function startTracing(
  traces: OtlpExport | undefined,
  resource: ResourceAttributes,
  spanProcessors?: SpanProcessor[],
): Tracing {
  installPropagation();
  const processors =
    spanProcessors ??
    (traces === undefined
      ? []
      : [
          new BatchSpanProcessor({
            exporter: otlpSpanExporter(traces),
            ...SPAN_EXPORT_LIMITS,
          }),
        ]);
  if (processors.length === 0) {
    return { enabled: false, shutdown: async () => {} };
  }
  const provider = new TracerProvider({
    resource: resourceFromAttributes(resource),
    // The SDK default, set explicitly: every span of a sampled parent and
    // every root span is recorded.
    sampler: new ParentBasedSampler({ root: new AlwaysOnSampler() }),
    spanProcessors: processors,
  });
  trace.setGlobalTracerProvider(provider);
  return {
    enabled: true,
    shutdown: async () => {
      try {
        await provider.shutdown();
      } finally {
        trace.disable();
      }
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

const headersSetter: TextMapSetter<Record<string, string>> = {
  set: (carrier, key, value) => {
    carrier[key] = value;
  },
};

/** Parent context from W3C headers of an incoming request or message. */
export const extractContext = (
  headers: Headers | Readonly<Record<string, string>>,
): Context =>
  headers instanceof Headers
    ? propagation.extract(ROOT_CONTEXT, headers, fetchHeadersGetter)
    : propagation.extract(ROOT_CONTEXT, headers, headersGetter);

/** Writes `traceparent`/`tracestate` (and allowed baggage) of `ctx`. */
export const injectContext = (
  ctx: Context,
  headers: Record<string, string>,
): void => {
  propagation.inject(ctx, headers, headersSetter);
};

/**
 * Marks `span` as failed with a safe description: the error type (stable
 * `code` or class name), never the message, which may contain the input
 * (docs/observability.md, "Логи" and "Use cases").
 */
export function markSpanError(span: Span, error: unknown): void {
  const type = errorType(error);
  span.setAttribute("error.type", type);
  span.setStatus({ code: SpanStatusCode.ERROR, message: type });
}

/**
 * Runs `work` inside a new active span that continues `parent`; an error
 * marks the span (type only) and is rethrown. The span ends when `work`
 * settles.
 */
export async function withSpan<T>(
  name: string,
  options: Readonly<{
    parent: Context;
    kind: SpanKind;
    attributes?: Attributes;
    links?: Link[];
  }>,
  work: (span: Span) => Promise<T>,
): Promise<T> {
  const tracer = trace.getTracer(TRACER_NAME);
  return tracer.startActiveSpan(
    name,
    {
      kind: options.kind,
      attributes: options.attributes ?? {},
      ...(options.links === undefined ? {} : { links: options.links }),
    },
    options.parent,
    async (span) => {
      try {
        return await work(span);
      } catch (error) {
        markSpanError(span, error);
        throw error;
      } finally {
        span.end();
      }
    },
  );
}

export { SpanKind };

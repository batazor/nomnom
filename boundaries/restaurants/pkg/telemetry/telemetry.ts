// OpenTelemetry bootstrap of a restaurants process (ADR 9,
// docs/observability.md, docs/cloud-native-runtime.md#observability-lifecycle),
// configured only by TelemetryConfig from the configuration file: OTEL_* is
// not read. Metrics and traces share one resource (pkg/telemetry/resource.ts).
// OTLP metrics are exported only with `spec.metrics.enabled`; without it the
// global no-op MeterProvider stays in place and instruments cost nothing. The
// pipeline adds Node.js runtime metrics (v8js.*, nodejs.eventloop.*) and
// process.cpu.time / process.memory.usage. Tracing (pkg/telemetry/tracing.ts)
// always propagates W3C Trace Context and exports spans only with
// `spec.tracing.enabled`. Shutdown performs the final export of both providers
// within the flush timeout.
import { metrics } from "@opentelemetry/api";
import { RuntimeNodeInstrumentation } from "@opentelemetry/instrumentation-runtime-node";
import { resourceFromAttributes } from "@opentelemetry/resources";
import {
  type IMetricReader,
  MeterProvider,
  PeriodicExportingMetricReader,
} from "@opentelemetry/sdk-metrics";
import type { SpanProcessor } from "@opentelemetry/sdk-trace";

import type { TelemetryConfig } from "../config/telemetry.ts";
import { OTLP_EXPORT_TIMEOUT_MS, otlpMetricExporter } from "./otlp.ts";
import { registerProcessMetrics } from "./process-metrics.ts";
import { type ResourceAttributes, telemetryResource } from "./resource.ts";
import { startTracing } from "./tracing.ts";

/**
 * Sampling interval of the event loop delay histogram (runtime-node, its
 * default): a gauge is reported only after five samples per export.
 */
const RUNTIME_MONITORING_PRECISION_MS = 10;

/** Default of `spec.shutdown.telemetryFlushTimeout`. */
export const DEFAULT_TELEMETRY_FLUSH_TIMEOUT_MS = 5000;

export type Telemetry = Readonly<{
  /** OTLP metrics are exported. */
  enabled: boolean;
  /** OTLP traces are exported. */
  tracing: boolean;
  /** Resource shared by traces, metrics and logs. */
  resource: ResourceAttributes;
  /**
   * Final export of traces and metrics. Resolves within `timeoutMs` even when
   * the collector is unavailable: telemetry never blocks process exit.
   */
  shutdown(timeoutMs?: number): Promise<void>;
}>;

export type TelemetryOptions = Readonly<{
  /** Metric readers instead of the OTLP exporter (tests). */
  metricReaders?: IMetricReader[];
  /** Span processors instead of the OTLP exporter (tests). */
  spanProcessors?: SpanProcessor[];
}>;

export function startTelemetry(
  config: TelemetryConfig,
  options: TelemetryOptions = {},
): Telemetry {
  const resource = telemetryResource(config);
  const tracing = startTracing(config.traces, resource, options.spanProcessors);
  const readers =
    options.metricReaders ??
    (config.metrics === undefined
      ? []
      : [
          new PeriodicExportingMetricReader({
            exporter: otlpMetricExporter(config.metrics),
            exportIntervalMillis: config.metrics.exportIntervalMs,
            // The reader requires the export deadline within the interval.
            exportTimeoutMillis: Math.min(
              OTLP_EXPORT_TIMEOUT_MS,
              config.metrics.exportIntervalMs,
            ),
          }),
        ]);
  if (readers.length === 0) {
    return {
      enabled: false,
      tracing: tracing.enabled,
      resource,
      shutdown: (timeoutMs) =>
        withTimeout(
          tracing.shutdown(),
          timeoutMs ?? DEFAULT_TELEMETRY_FLUSH_TIMEOUT_MS,
        ),
    };
  }
  const provider = new MeterProvider({
    resource: resourceFromAttributes(resource),
    readers,
  });
  metrics.setGlobalMeterProvider(provider);
  const runtime = new RuntimeNodeInstrumentation({
    monitoringPrecision: RUNTIME_MONITORING_PRECISION_MS,
  });
  runtime.setMeterProvider(provider);
  runtime.enable();
  registerProcessMetrics(provider.getMeter("nomnom.restaurants.process"));
  return {
    enabled: true,
    tracing: tracing.enabled,
    resource,
    shutdown: async (timeoutMs) => {
      runtime.disable();
      try {
        await withTimeout(
          Promise.allSettled([provider.shutdown(), tracing.shutdown()]).then(
            () => {},
          ),
          timeoutMs ?? DEFAULT_TELEMETRY_FLUSH_TIMEOUT_MS,
        );
      } finally {
        metrics.disable();
      }
    },
  };
}

async function withTimeout(work: Promise<void>, timeoutMs: number) {
  let timer: NodeJS.Timeout | undefined;
  const expired = new Promise<void>((resolve) => {
    timer = setTimeout(resolve, timeoutMs);
    timer.unref();
  });
  try {
    await Promise.race([work.catch(() => {}), expired]);
  } finally {
    clearTimeout(timer);
  }
}

// OpenTelemetry bootstrap of a restaurants process (ADR 9,
// docs/observability.md, docs/cloud-native-runtime.md#observability-lifecycle).
// Metrics and traces share one resource (pkg/telemetry/resource.ts). OTLP
// metrics are exported only when OTEL_EXPORTER_OTLP_ENDPOINT or
// OTEL_EXPORTER_OTLP_METRICS_ENDPOINT is set; without it the global no-op
// MeterProvider stays in place and instruments cost nothing. The pipeline adds
// Node.js runtime metrics (v8js.*, nodejs.eventloop.*) and process.cpu.time /
// process.memory.usage. Tracing (pkg/telemetry/tracing.ts) always propagates
// W3C Trace Context and exports spans only with a traces endpoint. Shutdown
// performs the final export of both providers within the flush timeout.
import { metrics } from "@opentelemetry/api";
import { OTLPMetricExporter } from "@opentelemetry/exporter-metrics-otlp-grpc";
import { RuntimeNodeInstrumentation } from "@opentelemetry/instrumentation-runtime-node";
import { resourceFromAttributes } from "@opentelemetry/resources";
import {
  type IMetricReader,
  MeterProvider,
  PeriodicExportingMetricReader,
} from "@opentelemetry/sdk-metrics";
import type { SpanProcessor } from "@opentelemetry/sdk-trace";

import { registerProcessMetrics } from "./process-metrics.ts";
import {
  type Environment,
  type ResourceAttributes,
  telemetryResource,
} from "./resource.ts";
import { startTracing } from "./tracing.ts";

export const OTLP_ENDPOINT_ENV = "OTEL_EXPORTER_OTLP_ENDPOINT";
const OTLP_METRICS_ENDPOINT_ENV = "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT";

/** Interval of the periodic OTLP metrics export. */
const METRICS_EXPORT_INTERVAL_MS = 15_000;
/** Deadline of one metrics export; below the interval. */
const METRICS_EXPORT_TIMEOUT_MS = 10_000;
/**
 * Sampling interval of the event loop delay histogram (runtime-node, its
 * default): a gauge is reported only after five samples per export.
 */
const RUNTIME_MONITORING_PRECISION_MS = 10;

/** Default of `shutdown.telemetry_flush_timeout`. */
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

export const metricsEndpointConfigured = (environment: Environment): boolean =>
  (environment[OTLP_ENDPOINT_ENV]?.trim() ?? "") !== "" ||
  (environment[OTLP_METRICS_ENDPOINT_ENV]?.trim() ?? "") !== "";

export function startTelemetry(
  serviceName: string,
  environment: Environment = process.env,
  options: TelemetryOptions = {},
): Telemetry {
  const resource = telemetryResource(serviceName, environment);
  const tracing = startTracing(
    serviceName,
    environment,
    options.spanProcessors === undefined
      ? {}
      : { spanProcessors: options.spanProcessors },
  );
  const readers =
    options.metricReaders ??
    (metricsEndpointConfigured(environment)
      ? // The exporter reads endpoint, headers and protocol options from OTEL_*.
        [
          new PeriodicExportingMetricReader({
            exporter: new OTLPMetricExporter(),
            exportIntervalMillis: METRICS_EXPORT_INTERVAL_MS,
            exportTimeoutMillis: METRICS_EXPORT_TIMEOUT_MS,
          }),
        ]
      : []);
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

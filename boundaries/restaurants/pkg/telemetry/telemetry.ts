// OpenTelemetry bootstrap (ADR 13 observability, docs/observability.md).
// OTLP metrics export is enabled only when OTEL_EXPORTER_OTLP_ENDPOINT is set;
// without it the global no-op MeterProvider stays in place and instruments
// cost nothing. Tracing (pkg/telemetry/tracing.ts) always propagates W3C
// Trace Context and exports spans only with a traces endpoint.
import { metrics } from "@opentelemetry/api";
import { OTLPMetricExporter } from "@opentelemetry/exporter-metrics-otlp-grpc";
import { resourceFromAttributes } from "@opentelemetry/resources";
import {
  MeterProvider,
  PeriodicExportingMetricReader,
} from "@opentelemetry/sdk-metrics";

import { startTracing } from "./tracing.ts";

export const OTLP_ENDPOINT_ENV = "OTEL_EXPORTER_OTLP_ENDPOINT";

export type Telemetry = Readonly<{
  /** OTLP metrics are exported. */
  enabled: boolean;
  /** OTLP traces are exported. */
  tracing: boolean;
  shutdown(): Promise<void>;
}>;

export function startTelemetry(
  serviceName: string,
  environment: Record<string, string | undefined> = process.env,
): Telemetry {
  const tracing = startTracing(serviceName, environment);
  if ((environment[OTLP_ENDPOINT_ENV]?.trim() ?? "") === "") {
    return {
      enabled: false,
      tracing: tracing.enabled,
      shutdown: () => tracing.shutdown(),
    };
  }
  // The exporter reads endpoint, headers and protocol options from OTEL_*.
  const provider = new MeterProvider({
    resource: resourceFromAttributes({
      "service.name": environment.OTEL_SERVICE_NAME?.trim() || serviceName,
      "service.namespace": "nomnom",
    }),
    readers: [
      new PeriodicExportingMetricReader({
        exporter: new OTLPMetricExporter(),
        exportIntervalMillis: 15_000,
      }),
    ],
  });
  metrics.setGlobalMeterProvider(provider);
  return {
    enabled: true,
    tracing: tracing.enabled,
    shutdown: async () => {
      await Promise.all([provider.shutdown(), tracing.shutdown()]);
      metrics.disable();
    },
  };
}

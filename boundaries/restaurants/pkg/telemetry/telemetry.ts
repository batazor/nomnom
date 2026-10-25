// OpenTelemetry metrics bootstrap (ADR 13 observability, docs/observability.md).
// OTLP export is enabled only when OTEL_EXPORTER_OTLP_ENDPOINT is set; without
// it the global no-op MeterProvider stays in place and instruments cost nothing.
import { metrics } from "@opentelemetry/api";
import { OTLPMetricExporter } from "@opentelemetry/exporter-metrics-otlp-grpc";
import { resourceFromAttributes } from "@opentelemetry/resources";
import {
  MeterProvider,
  PeriodicExportingMetricReader,
} from "@opentelemetry/sdk-metrics";

export const OTLP_ENDPOINT_ENV = "OTEL_EXPORTER_OTLP_ENDPOINT";

export type Telemetry = Readonly<{
  enabled: boolean;
  shutdown(): Promise<void>;
}>;

export function startTelemetry(
  serviceName: string,
  environment: Record<string, string | undefined> = process.env,
): Telemetry {
  if ((environment[OTLP_ENDPOINT_ENV]?.trim() ?? "") === "") {
    return { enabled: false, shutdown: async () => {} };
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
    shutdown: async () => {
      await provider.shutdown();
      metrics.disable();
    },
  };
}

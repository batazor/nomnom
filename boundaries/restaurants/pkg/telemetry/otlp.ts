// OTLP exporters of traces and metrics by `spec.telemetry.protocol` (ADR 9):
// `grpc` or `http/protobuf`. Endpoint, protocol, credentials, compression,
// timeout and temporality are passed explicitly, so the exporters' own
// OTEL_EXPORTER_OTLP_* fallbacks for them never apply.
import { credentials } from "@grpc/grpc-js";
import { OTLPMetricExporter as GrpcMetricExporter } from "@opentelemetry/exporter-metrics-otlp-grpc";
import { OTLPMetricExporter as ProtobufMetricExporter } from "@opentelemetry/exporter-metrics-otlp-proto";
import { OTLPTraceExporter as GrpcTraceExporter } from "@opentelemetry/exporter-trace-otlp-grpc";
import { OTLPTraceExporter as ProtobufTraceExporter } from "@opentelemetry/exporter-trace-otlp-proto";
import { CompressionAlgorithm } from "@opentelemetry/otlp-exporter-base";
import {
  AggregationTemporality,
  type PushMetricExporter,
} from "@opentelemetry/sdk-metrics";
import type { SpanExporter } from "@opentelemetry/sdk-trace";

import type { OtlpExport } from "../config/telemetry.ts";

/** Deadline of one export request (the OTLP exporter default). */
export const OTLP_EXPORT_TIMEOUT_MS = 10_000;

/** Plaintext gRPC for `http://`, TLS for `https://`, as the endpoint says. */
const grpcCredentials = (endpoint: string) =>
  endpoint.startsWith("https://")
    ? credentials.createSsl()
    : credentials.createInsecure();

/**
 * OTLP/HTTP URL of a signal: the endpoint is the base of the collector, as
 * OTEL_EXPORTER_OTLP_ENDPOINT, and the signal path is appended to it.
 */
export const signalUrl = (endpoint: string, path: "v1/traces" | "v1/metrics") =>
  new URL(path, endpoint.endsWith("/") ? endpoint : `${endpoint}/`).href;

export function otlpSpanExporter(config: OtlpExport): SpanExporter {
  const common = {
    compression: CompressionAlgorithm.NONE,
    timeoutMillis: OTLP_EXPORT_TIMEOUT_MS,
  };
  switch (config.protocol) {
    case "grpc":
      return new GrpcTraceExporter({
        ...common,
        url: config.endpoint,
        credentials: grpcCredentials(config.endpoint),
      });
    case "http/protobuf":
      return new ProtobufTraceExporter({
        ...common,
        url: signalUrl(config.endpoint, "v1/traces"),
      });
    default:
      throw new Error(
        `unknown OTLP protocol "${String(config.protocol)}", expected grpc or http/protobuf`,
      );
  }
}

export function otlpMetricExporter(config: OtlpExport): PushMetricExporter {
  const common = {
    compression: CompressionAlgorithm.NONE,
    timeoutMillis: OTLP_EXPORT_TIMEOUT_MS,
    temporalityPreference: AggregationTemporality.CUMULATIVE,
  };
  switch (config.protocol) {
    case "grpc":
      return new GrpcMetricExporter({
        ...common,
        url: config.endpoint,
        credentials: grpcCredentials(config.endpoint),
      });
    case "http/protobuf":
      return new ProtobufMetricExporter({
        ...common,
        url: signalUrl(config.endpoint, "v1/metrics"),
      });
    default:
      throw new Error(
        `unknown OTLP protocol "${String(config.protocol)}", expected grpc or http/protobuf`,
      );
  }
}

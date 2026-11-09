import { z } from "zod";

import {
  DEFAULT_LOG_LEVEL,
  DEFAULT_METRICS_EXPORT_INTERVAL,
  DEFAULT_TELEMETRY_PROTOCOL,
} from "./defaults.ts";
import type { Spec } from "./file.ts";
import { optionalStringSchema, positiveDurationSchema } from "./schemas.ts";

/** `service.instance.id` of the resource: pod name from the Downward API. */
export const POD_NAME_ENV = "POD_NAME";

/** Resource attributes the process sets itself; the file must not set them. */
const PROCESS_RESOURCE_ATTRIBUTES = ["service.name", "service.instance.id"];

/** `spec.telemetry`: OTLP collector and resource (ADR 9). */
export const telemetrySectionSchema = z
  .strictObject({
    /** URL OTLP collector; обязателен, если включены traces или metrics. */
    endpoint: optionalStringSchema,
    protocol: z
      .enum(["grpc", "http/protobuf"], {
        error: "expected grpc or http/protobuf",
      })
      .prefault(DEFAULT_TELEMETRY_PROTOCOL),
    /** `service.name`; по умолчанию имя процесса. */
    serviceName: z.string().trim().min(1, "must not be empty").optional(),
    resourceAttributes: z
      .record(z.string(), z.string())
      .superRefine((attributes, context) => {
        for (const key of PROCESS_RESOURCE_ATTRIBUTES) {
          if (Object.hasOwn(attributes, key)) {
            context.addIssue({
              code: "custom",
              path: [key],
              message: "must not be set: the process sets it itself",
            });
          }
        }
      })
      .prefault({}),
  })
  .prefault({});

/** `spec.tracing`: export of spans. */
export const tracingSectionSchema = z
  .strictObject({ enabled: z.boolean().prefault(false) })
  .prefault({});

/** `spec.metrics`: periodic export of metrics. */
export const metricsSectionSchema = z
  .strictObject({
    enabled: z.boolean().prefault(false),
    exportInterval: positiveDurationSchema.prefault(
      DEFAULT_METRICS_EXPORT_INTERVAL,
    ),
  })
  .prefault({});

/** `spec.logging`: JSON logs to stdout. */
export const loggingSectionSchema = z
  .strictObject({
    level: z
      .enum(["debug", "info", "warn", "error"], {
        error: "expected debug, info, warn or error",
      })
      .prefault(DEFAULT_LOG_LEVEL),
  })
  .prefault({});

/** Severity of the logger (pkg/telemetry/logger.ts) per `spec.logging.level`. */
const SEVERITY = {
  debug: "DEBUG",
  info: "INFO",
  warn: "WARN",
  error: "ERROR",
} as const;

export type OtlpProtocol = "grpc" | "http/protobuf";

/** OTLP export of one signal. */
export type OtlpExport = Readonly<{
  /** Base URL of the collector, e.g. `http://alloy:4317`. */
  endpoint: string;
  protocol: OtlpProtocol;
}>;

/**
 * OpenTelemetry of a process (ADR 9): the resource, the signals exported over
 * OTLP and the log level. Nothing is read from `OTEL_*`.
 */
export type TelemetryConfig = Readonly<{
  serviceName: string;
  /** `service.instance.id`: the pod name. */
  instanceId: string;
  resourceAttributes: Readonly<Record<string, string>>;
  /** Span export; `undefined` — only W3C context propagation. */
  traces: OtlpExport | undefined;
  /** Metrics export; `undefined` — the no-op MeterProvider stays. */
  metrics: (OtlpExport & Readonly<{ exportIntervalMs: number }>) | undefined;
  logLevel: (typeof SEVERITY)[keyof typeof SEVERITY];
}>;

export function loadTelemetryConfig(
  spec: Pick<Spec, "telemetry" | "tracing" | "metrics" | "logging">,
  environment: Readonly<Record<string, string | undefined>>,
  defaultServiceName: string,
): TelemetryConfig {
  const instanceId = environment[POD_NAME_ENV]?.trim() ?? "";
  if (instanceId === "") {
    throw new Error(
      `${POD_NAME_ENV} is required: it is service.instance.id of the telemetry resource`,
    );
  }
  const { telemetry, tracing, metrics } = spec;
  const endpoint = telemetry.endpoint ?? "";
  if (endpoint === "" && (tracing.enabled || metrics.enabled)) {
    throw new Error(
      "spec.telemetry.endpoint is required when spec.tracing.enabled or spec.metrics.enabled is true",
    );
  }
  if (endpoint !== "" && !/^https?:\/\/[^/]/.test(endpoint)) {
    throw new Error(
      `spec.telemetry.endpoint: "${endpoint}" must be an http(s) url`,
    );
  }
  const otlp = { endpoint, protocol: telemetry.protocol };
  return Object.freeze({
    serviceName: telemetry.serviceName ?? defaultServiceName,
    instanceId,
    resourceAttributes: Object.freeze({ ...telemetry.resourceAttributes }),
    traces: tracing.enabled ? Object.freeze(otlp) : undefined,
    metrics: metrics.enabled
      ? Object.freeze({ ...otlp, exportIntervalMs: metrics.exportInterval })
      : undefined,
    logLevel: SEVERITY[spec.logging.level],
  });
}

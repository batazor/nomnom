// OpenTelemetry resource of a restaurants process (docs/observability.md,
// "Единая идентификация сервисов"): one set of attributes for traces, metrics
// and stdout logs, built only from the configuration file (TelemetryConfig) —
// OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES are not read.
// `service.name` defaults to the process name, `service.instance.id` is the
// pod name and `service.namespace` is always `nomnom`.
import { SDK_INFO } from "@opentelemetry/core";

import type { TelemetryConfig } from "../config/telemetry.ts";

export const SERVICE_NAMESPACE = "nomnom";

export type ResourceAttributes = Readonly<Record<string, string>>;

/** Resource attributes of the process. */
export function telemetryResource(
  config: Pick<
    TelemetryConfig,
    "serviceName" | "instanceId" | "resourceAttributes"
  >,
): ResourceAttributes {
  return Object.freeze({
    "telemetry.sdk.name": String(SDK_INFO["telemetry.sdk.name"]),
    "telemetry.sdk.language": String(SDK_INFO["telemetry.sdk.language"]),
    "telemetry.sdk.version": String(SDK_INFO["telemetry.sdk.version"]),
    "process.runtime.name": "nodejs",
    "process.runtime.version": process.versions.node,
    ...config.resourceAttributes,
    "service.name": config.serviceName,
    "service.instance.id": config.instanceId,
    "service.namespace": SERVICE_NAMESPACE,
  });
}

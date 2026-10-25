// OpenTelemetry resource of a restaurants process (docs/observability.md,
// "Единая идентификация сервисов"): one set of attributes for traces, metrics
// and stdout logs. `service.name` defaults to the process name; precedence
// follows the SDK: OTEL_SERVICE_NAME > OTEL_RESOURCE_ATTRIBUTES > default.
// `service.namespace` is always `nomnom`.
import { SDK_INFO } from "@opentelemetry/core";

export const SERVICE_NAMESPACE = "nomnom";

export type Environment = Readonly<Record<string, string | undefined>>;

export type ResourceAttributes = Readonly<Record<string, string>>;

/**
 * `key=value` pairs of OTEL_RESOURCE_ATTRIBUTES (comma separated, values
 * percent-encoded); malformed pairs are skipped as the SDK does.
 */
export function parseResourceAttributes(
  raw: string | undefined,
): Record<string, string> {
  const result: Record<string, string> = {};
  for (const pair of (raw ?? "").split(",")) {
    const separator = pair.indexOf("=");
    if (separator <= 0) continue;
    const key = pair.slice(0, separator).trim();
    const value = pair.slice(separator + 1).trim();
    if (key === "" || value === "") continue;
    try {
      result[key] = decodeURIComponent(value);
    } catch {
      // A value that is not valid percent-encoding is dropped.
    }
  }
  return result;
}

/** Resource attributes of the process `serviceName`. */
export function telemetryResource(
  serviceName: string,
  environment: Environment = process.env,
): ResourceAttributes {
  const fromEnvironment = parseResourceAttributes(
    environment.OTEL_RESOURCE_ATTRIBUTES,
  );
  const name = environment.OTEL_SERVICE_NAME?.trim();
  return Object.freeze({
    "telemetry.sdk.name": String(SDK_INFO["telemetry.sdk.name"]),
    "telemetry.sdk.language": String(SDK_INFO["telemetry.sdk.language"]),
    "telemetry.sdk.version": String(SDK_INFO["telemetry.sdk.version"]),
    "process.runtime.name": "nodejs",
    "process.runtime.version": process.versions.node,
    "service.name": serviceName,
    ...fromEnvironment,
    ...(name ? { "service.name": name } : {}),
    "service.namespace": SERVICE_NAMESPACE,
  });
}

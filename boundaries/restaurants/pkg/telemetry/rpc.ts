// Semconv RPC metrics (OpenTelemetry semantic conventions v1.44.0, the same
// as otelgrpc in orders): `rpc.server.call.duration` and
// `rpc.client.call.duration` in seconds (base2 exponential histograms, view in
// pkg/telemetry/histograms.ts),
// `rpc.method` as `Service/Method` and `rpc.response.status_code` as the name
// of the canonical gRPC code. Inventory: ops/grafana/inventory, section rpc.
import { type Histogram, metrics } from "@opentelemetry/api";

export const RPC_INSTRUMENTATION = "nomnom.restaurants.rpc";

/** Canonical gRPC status names by numeric code. */
export const GRPC_STATUS_NAMES: readonly string[] = [
  "OK",
  "CANCELLED",
  "UNKNOWN",
  "INVALID_ARGUMENT",
  "DEADLINE_EXCEEDED",
  "NOT_FOUND",
  "ALREADY_EXISTS",
  "PERMISSION_DENIED",
  "RESOURCE_EXHAUSTED",
  "FAILED_PRECONDITION",
  "ABORTED",
  "OUT_OF_RANGE",
  "UNIMPLEMENTED",
  "INTERNAL",
  "UNAVAILABLE",
  "DATA_LOSS",
  "UNAUTHENTICATED",
];

export const grpcStatusName = (code: number): string =>
  GRPC_STATUS_NAMES[code] ?? "UNKNOWN";

/**
 * Codes that mark a server span as failed (semconv gRPC): the server could
 * not serve the call. Client errors such as NOT_FOUND leave it unset.
 */
const SERVER_ERROR_CODES = new Set([2, 4, 12, 13, 14, 15]);

export const isServerError = (code: number): boolean =>
  SERVER_ERROR_CODES.has(code);

let serverDuration: Histogram | undefined;
let clientDuration: Histogram | undefined;
let meterProvider: unknown;

/**
 * Histograms on the global MeterProvider, recreated when it changes (the
 * provider is registered by startTelemetry after modules are loaded).
 */
function instruments(): { server: Histogram; client: Histogram } {
  const provider = metrics.getMeterProvider();
  if (
    serverDuration === undefined ||
    clientDuration === undefined ||
    provider !== meterProvider
  ) {
    meterProvider = provider;
    const meter = metrics.getMeter(RPC_INSTRUMENTATION);
    serverDuration = meter.createHistogram("rpc.server.call.duration", {
      description: "Measures the duration of inbound remote procedure calls.",
      unit: "s",
    });
    clientDuration = meter.createHistogram("rpc.client.call.duration", {
      description: "Measures the duration of outbound remote procedure calls.",
      unit: "s",
    });
  }
  return { server: serverDuration, client: clientDuration };
}

export type RpcSystem = "grpc" | "connect_rpc";

export function recordServerCall(
  durationS: number,
  attributes: Readonly<{ system: RpcSystem; method: string; code: number }>,
): void {
  instruments().server.record(durationS, {
    "rpc.system.name": attributes.system,
    "rpc.method": attributes.method,
    "rpc.response.status_code": grpcStatusName(attributes.code),
  });
}

export function recordClientCall(
  durationS: number,
  attributes: Readonly<{
    method: string;
    code: number;
    serverAddress: string;
    serverPort?: number;
  }>,
): void {
  const status = grpcStatusName(attributes.code);
  instruments().client.record(durationS, {
    "rpc.system.name": "grpc",
    "rpc.method": attributes.method,
    "rpc.response.status_code": status,
    "server.address": attributes.serverAddress,
    ...(attributes.serverPort === undefined
      ? {}
      : { "server.port": attributes.serverPort }),
    ...(attributes.code === 0 ? {} : { "error.type": status }),
  });
}

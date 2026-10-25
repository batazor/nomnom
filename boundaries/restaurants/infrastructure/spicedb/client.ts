// SpiceDB client of the `restaurants` adapters: the narrow call interfaces,
// the production factory, the per-call deadline and check consistency. The
// access-control, relationship and reconciliation adapters share it.
import { deadlineInterceptor, v1 } from "@authzed/authzed-node";

import type { SpiceDbEnabledConfig } from "../../pkg/config/config.ts";
import {
  clientTelemetryInterceptor,
  grpcPeer,
} from "../grpc/client-telemetry.ts";

/**
 * The single SpiceDB call the adapter needs. The production implementation
 * is the `@authzed/authzed-node` promise client; tests pass a fake.
 */
export interface PermissionsClient {
  checkPermission(
    request: v1.CheckPermissionRequest,
  ): Promise<v1.CheckPermissionResponse>;
}

/**
 * Relationship reads and writes of the `restaurants` writers and the
 * reconciler. `readRelationships` returns one page (the stream collected).
 */
export interface RelationshipsClient {
  writeRelationships(
    request: v1.WriteRelationshipsRequest,
  ): Promise<v1.WriteRelationshipsResponse>;
  readRelationships(
    request: v1.ReadRelationshipsRequest,
  ): Promise<v1.ReadRelationshipsResponse[]>;
}

/** Closable production client created by {@link createSpiceDbClient}. */
export interface SpiceDbClient extends PermissionsClient, RelationshipsClient {
  close(): void;
}

/**
 * Creates the SpiceDB client: preshared key as bearer token, TLS unless
 * `insecure` (plaintext for kind and local runs only), and a per-call gRPC
 * deadline of `timeoutMs`. Every call gets a CLIENT span with the W3C
 * context in its metadata and `rpc.client.call.duration`; the span covers the
 * deadline interceptor.
 */
export function createSpiceDbClient(
  config: SpiceDbEnabledConfig,
): SpiceDbClient {
  const client = v1.NewClient(
    config.presharedKey,
    config.endpoint,
    config.insecure
      ? v1.ClientSecurity.INSECURE_PLAINTEXT_CREDENTIALS
      : v1.ClientSecurity.SECURE,
    undefined,
    {
      interceptors: [
        clientTelemetryInterceptor(grpcPeer(config.endpoint)),
        deadlineInterceptor(config.timeoutMs),
      ],
    },
  );
  return {
    checkPermission: (request) => client.promises.checkPermission(request),
    writeRelationships: (request) =>
      client.promises.writeRelationships(request),
    readRelationships: (request) => client.promises.readRelationships(request),
    close: () => client.close(),
  };
}

/**
 * Rejects when `call` does not settle within `timeoutMs`; the gRPC deadline
 * interceptor bounds the call itself, this bounds clients without it.
 */
export async function withDeadline<T>(
  call: Promise<T>,
  timeoutMs: number,
  operation: string,
): Promise<T> {
  let timer: NodeJS.Timeout | undefined;
  const timeout = new Promise<never>((_, reject) => {
    timer = setTimeout(
      () =>
        reject(
          new Error(`SpiceDB ${operation} exceeded ${timeoutMs}ms deadline`),
        ),
      timeoutMs,
    );
  });
  try {
    return await Promise.race([call, timeout]);
  } finally {
    clearTimeout(timer);
  }
}

/**
 * Consistency of a check: `at_least_as_fresh` with a stored revision,
 * otherwise `minimize_latency`.
 */
export function checkConsistency(revision: string | undefined): v1.Consistency {
  return v1.Consistency.create({
    requirement:
      revision === undefined || revision === ""
        ? { oneofKind: "minimizeLatency", minimizeLatency: true }
        : {
            oneofKind: "atLeastAsFresh",
            atLeastAsFresh: v1.ZedToken.create({ token: revision }),
          },
  });
}

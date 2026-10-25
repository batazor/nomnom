// Authorization check metrics (ADR 18; ops/semconv/model/authz.yaml):
// `nomnom.authz.checks` and `nomnom.authz.check.duration` per SpiceDB
// CheckPermission by permission, resource type and result. `denied` is a
// business result, `error` and `timeout` are technical failures. Subject and
// resource IDs never become attributes.
import { v1 } from "@authzed/authzed-node";
import { status } from "@grpc/grpc-js";
import { type Meter, metrics } from "@opentelemetry/api";

import type { PermissionsClient } from "./client.ts";

export const AUTHZ_INSTRUMENTATION = "nomnom.restaurants.authz";

/** Seconds: a check is one SpiceDB round trip bounded by its deadline. */
export const AUTHZ_DURATION_BUCKETS = [
  0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5,
];

// Attributes from the registry ops/semconv (make semconv-check).
const PERMISSION = "nomnom.authz.permission";
const RESOURCE_TYPE = "nomnom.authz.resource_type";
const RESULT = "nomnom.authz.result";

export type AuthorizationResult = "allowed" | "denied" | "error" | "timeout";

/**
 * {@link PermissionsClient} that records every check. Only
 * `PERMISSIONSHIP_HAS_PERMISSION` is `allowed`, like the access-control
 * adapter; a gRPC DEADLINE_EXCEEDED is `timeout`, any other failure `error`.
 * The response and the error are passed through unchanged. Instruments live
 * on the global MeterProvider by default and are no-op until telemetry starts.
 */
export class ObservedPermissionsClient implements PermissionsClient {
  readonly #inner: PermissionsClient;
  readonly #checks;
  readonly #duration;

  constructor(
    inner: PermissionsClient,
    meter: Meter = metrics.getMeter(AUTHZ_INSTRUMENTATION),
  ) {
    this.#inner = inner;
    this.#checks = meter.createCounter("nomnom.authz.checks", {
      description: "Authorization checks by result.",
      unit: "{check}",
    });
    this.#duration = meter.createHistogram("nomnom.authz.check.duration", {
      description: "Duration of one authorization check.",
      unit: "s",
      advice: { explicitBucketBoundaries: AUTHZ_DURATION_BUCKETS },
    });
  }

  async checkPermission(
    request: v1.CheckPermissionRequest,
  ): Promise<v1.CheckPermissionResponse> {
    const resourceType = request.resource?.objectType;
    if (resourceType === undefined) {
      throw new Error("CheckPermission request without a resource");
    }
    const started = performance.now();
    let result: AuthorizationResult = "error";
    try {
      const response = await this.#inner.checkPermission(request);
      result =
        response.permissionship ===
        v1.CheckPermissionResponse_Permissionship.HAS_PERMISSION
          ? "allowed"
          : "denied";
      return response;
    } catch (error) {
      result = isDeadlineExceeded(error) ? "timeout" : "error";
      throw error;
    } finally {
      const attributes = {
        [PERMISSION]: request.permission,
        [RESOURCE_TYPE]: resourceType,
        [RESULT]: result,
      };
      this.#checks.add(1, attributes);
      this.#duration.record((performance.now() - started) / 1000, attributes);
    }
  }
}

const isDeadlineExceeded = (error: unknown): boolean =>
  (error as { code?: unknown } | undefined)?.code === status.DEADLINE_EXCEEDED;

import { ConnectError, type Interceptor } from "@connectrpc/connect";

import {
  extractContext,
  SpanKind,
  withSpan,
} from "../../pkg/telemetry/tracing.ts";

/**
 * Server span of one Connect/gRPC call, the child of the caller's
 * `traceparent` (W3C Trace Context). The handler runs inside the span, so the
 * Outbox metadata of its commands carries the trace (pkg/telemetry/trace-context.ts).
 */
export const tracingInterceptor: Interceptor = (next) => async (request) => {
  const service = request.service.typeName;
  const method = request.method.name;
  return withSpan(
    `${service}/${method}`,
    {
      parent: extractContext(request.header),
      kind: SpanKind.SERVER,
      attributes: {
        "rpc.system": "grpc",
        "rpc.service": service,
        "rpc.method": method,
      },
    },
    async (span) => {
      try {
        const response = await next(request);
        span.setAttribute("rpc.grpc.status_code", 0);
        return response;
      } catch (error) {
        if (error instanceof ConnectError) {
          span.setAttribute("rpc.grpc.status_code", error.code);
        }
        throw error;
      }
    },
  );
};

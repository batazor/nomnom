import { ConnectError, type Interceptor } from "@connectrpc/connect";
import { SpanStatusCode, trace } from "@opentelemetry/api";

import {
  grpcStatusName,
  isServerError,
  type RpcSystem,
  recordServerCall,
} from "../../pkg/telemetry/rpc.ts";
import {
  extractContext,
  SpanKind,
  TRACER_NAME,
} from "../../pkg/telemetry/tracing.ts";

/** gRPC and gRPC-Web are `grpc`; the Connect protocol is `connect_rpc`. */
const rpcSystem = (contentType: string | null): RpcSystem =>
  contentType?.startsWith("application/grpc") ? "grpc" : "connect_rpc";

/**
 * Server span and `rpc.server.call.duration` of one Connect/gRPC call
 * (semconv RPC): the span is the child of the caller's `traceparent` (W3C
 * Trace Context) and the handler runs inside it, so the Outbox metadata of
 * its commands carries the trace (pkg/telemetry/trace-context.ts). Only
 * server-side codes mark the span as failed; error text is not recorded.
 */
export const tracingInterceptor: Interceptor = (next) => async (request) => {
  const method = `${request.service.typeName}/${request.method.name}`;
  const system = rpcSystem(request.header.get("content-type"));
  const tracer = trace.getTracer(TRACER_NAME);
  const started = performance.now();
  return tracer.startActiveSpan(
    method,
    {
      kind: SpanKind.SERVER,
      attributes: { "rpc.system.name": system, "rpc.method": method },
    },
    extractContext(request.header),
    async (span) => {
      let code = 0;
      try {
        return await next(request);
      } catch (error) {
        code = ConnectError.from(error).code;
        throw error;
      } finally {
        const status = grpcStatusName(code);
        span.setAttribute("rpc.response.status_code", status);
        if (isServerError(code)) {
          span.setAttribute("error.type", status);
          span.setStatus({ code: SpanStatusCode.ERROR, message: status });
        }
        span.end();
        recordServerCall((performance.now() - started) / 1000, {
          system,
          method,
          code,
        });
      }
    },
  );
};

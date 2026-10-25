// Client instrumentation of `@grpc/grpc-js` channels (the SpiceDB client of
// `@authzed/authzed-node`): a CLIENT span per call that continues the active
// context, W3C Trace Context injected into the call metadata and
// `rpc.client.call.duration` (pkg/telemetry/rpc.ts). Request and response
// messages and metadata values are never recorded.
import {
  InterceptingCall,
  type Interceptor,
  type Metadata,
  type StatusObject,
} from "@grpc/grpc-js";
import {
  context,
  propagation,
  SpanStatusCode,
  type TextMapSetter,
  trace,
} from "@opentelemetry/api";

import { grpcStatusName, recordClientCall } from "../../pkg/telemetry/rpc.ts";
import { SpanKind, TRACER_NAME } from "../../pkg/telemetry/tracing.ts";

const metadataSetter: TextMapSetter<Metadata> = {
  set: (metadata, key, value) => {
    metadata.set(key, value);
  },
};

export type GrpcPeer = Readonly<{ address: string; port?: number }>;

/** `host:port` of a gRPC target as `server.address` and `server.port`. */
export function grpcPeer(endpoint: string): GrpcPeer {
  const target = endpoint.replace(/^[a-z]+:\/\//i, "").replace(/^dns:\/*/, "");
  const match = /^(\[[^\]]+\]|[^:]+)(?::(\d+))?$/.exec(target);
  if (match === null) return { address: target };
  const port = match[2] === undefined ? undefined : Number(match[2]);
  return port === undefined
    ? { address: match[1] ?? target }
    : { address: match[1] ?? target, port };
}

export function clientTelemetryInterceptor(peer: GrpcPeer): Interceptor {
  return (options, nextCall) => {
    // `/authzed.api.v1.PermissionsService/CheckPermission` → Service/Method.
    const method = options.method_definition.path.replace(/^\//, "");
    const parent = context.active();
    const span = trace.getTracer(TRACER_NAME).startSpan(
      method,
      {
        kind: SpanKind.CLIENT,
        attributes: {
          "rpc.system.name": "grpc",
          "rpc.method": method,
          "server.address": peer.address,
          ...(peer.port === undefined ? {} : { "server.port": peer.port }),
        },
      },
      parent,
    );
    const started = performance.now();
    let ended = false;
    const end = (code: number) => {
      if (ended) return;
      ended = true;
      const status = grpcStatusName(code);
      span.setAttribute("rpc.response.status_code", status);
      if (code !== 0) {
        span.setAttribute("error.type", status);
        span.setStatus({ code: SpanStatusCode.ERROR, message: status });
      }
      span.end();
      recordClientCall((performance.now() - started) / 1000, {
        method,
        code,
        serverAddress: peer.address,
        ...(peer.port === undefined ? {} : { serverPort: peer.port }),
      });
    };
    return new InterceptingCall(nextCall(options), {
      start(metadata, _listener, next) {
        propagation.inject(
          trace.setSpan(parent, span),
          metadata,
          metadataSetter,
        );
        next(metadata, {
          onReceiveStatus(status: StatusObject, nextStatus) {
            end(status.code);
            nextStatus(status);
          },
        });
      },
      cancel(next) {
        end(1);
        next();
      },
    });
  };
}

import { createServer, type Http2Server } from "node:http2";

import {
  Code,
  ConnectError,
  type Interceptor,
  type ServiceImpl,
} from "@connectrpc/connect";
import { connectNodeAdapter } from "@connectrpc/connect-node";
import { type Logger, silentLogger } from "../../pkg/telemetry/logger.ts";
import { RestaurantsService } from "./gen/nomnom/restaurants/v1/service_pb.js";
import { tracingInterceptor } from "./tracing.ts";

/**
 * INTERNAL hides the cause from the caller (transportError), so it is written
 * here as one ERROR record; other codes are expected outcomes, not logged.
 */
const errorLoggingInterceptor =
  (logger: Logger): Interceptor =>
  (next) =>
  async (request) => {
    try {
      return await next(request);
    } catch (error) {
      const connect = ConnectError.from(error);
      if (connect.code === Code.Internal) {
        logger.error("rpc failed", {
          operation: `${request.service.typeName}/${request.method.name}`,
          error: connect.cause ?? error,
        });
      }
      throw error;
    }
  };

/**
 * HTTP/2 server of the Connect/gRPC API. Every call runs inside a server span
 * that continues the caller's W3C `traceparent`; INTERNAL errors are logged
 * with their cause.
 */
export const createGrpcServer = (
  service: ServiceImpl<typeof RestaurantsService>,
  logger: Logger = silentLogger,
): Http2Server =>
  createServer(
    connectNodeAdapter({
      interceptors: [tracingInterceptor, errorLoggingInterceptor(logger)],
      routes: (router) => router.service(RestaurantsService, service),
    }),
  );

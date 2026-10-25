import { createServer, type Http2Server } from "node:http2";

import type { ServiceImpl } from "@connectrpc/connect";
import { connectNodeAdapter } from "@connectrpc/connect-node";

import { RestaurantsService } from "./gen/nomnom/restaurants/v1/service_pb.js";
import { tracingInterceptor } from "./tracing.ts";

/**
 * HTTP/2 server of the Connect/gRPC API. Every call runs inside a server span
 * that continues the caller's W3C `traceparent`.
 */
export const createGrpcServer = (
  service: ServiceImpl<typeof RestaurantsService>,
): Http2Server =>
  createServer(
    connectNodeAdapter({
      interceptors: [tracingInterceptor],
      routes: (router) => router.service(RestaurantsService, service),
    }),
  );

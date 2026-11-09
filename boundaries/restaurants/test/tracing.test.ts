import assert from "node:assert/strict";
import { createServer } from "node:http";
import type { AddressInfo } from "node:net";
import test from "node:test";

import { createClient } from "@connectrpc/connect";
import { createGrpcTransport } from "@connectrpc/connect-node";
import { ROOT_CONTEXT } from "@opentelemetry/api";

import { RestaurantsService } from "../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import { createGrpcServer } from "../infrastructure/grpc/server.ts";
import { createRestaurantsService } from "../infrastructure/grpc/service.ts";
import { GrpcWorker } from "../infrastructure/grpc/worker.ts";
import { loadTelemetryConfig, specSchema } from "../pkg/config/config.ts";
import type { Outbox } from "../pkg/outbox/outbox.ts";
import { startTelemetry } from "../pkg/telemetry/telemetry.ts";
import { currentTraceMetadata } from "../pkg/telemetry/trace-context.ts";
import {
  installPropagation,
  SpanKind,
  withSpan,
} from "../pkg/telemetry/tracing.ts";
import {
  createInfrastructure,
  createRestaurantWorld,
  owner,
} from "./support/restaurants.ts";

const TRACE_ID = "4bf92f3577b34da6a3ce929d0e0e4736";
const CALLER_SPAN_ID = "00f067aa0ba902b7";
const TRACEPARENT = `00-${TRACE_ID}-${CALLER_SPAN_ID}-01`;

const unused = {
  execute: async (): Promise<never> => assert.fail("unexpected call"),
};

/**
 * Open Restaurant over a real HTTP/2 server (createGrpcServer, as
 * cmd/restaurants) with an Outbox that records the trace metadata DatabaseOutbox
 * would write (pkg/telemetry/trace-context.ts).
 */
const startServer = async () => {
  const infrastructure = await createInfrastructure();
  const metadata: { traceparent?: string }[] = [];
  const outbox: Outbox = {
    add: async (aggregate, events) => {
      metadata.push(currentTraceMetadata());
      await infrastructure.outbox.add(aggregate, events);
    },
  };
  const world = await createRestaurantWorld(infrastructure, { outbox });
  const restaurantId = await world.givenRestaurant("Pho Bar", { open: false });
  const service = createRestaurantsService({
    getRestaurant: unused,
    getMenu: unused,
    batchGetRestaurants: unused,
    batchGetMenus: unused,
    listRestaurants: unused,
    listOwnedRestaurants: unused,
    receiveOrderRequest: unused,
    cancelRestaurantOrder: unused,
    acceptRestaurantOrder: unused,
    rejectRestaurantOrder: unused,
    startPreparation: unused,
    markRestaurantOrderAssembled: unused,
    markRestaurantOrderReady: unused,
    reportPreparationDelay: unused,
    reportPreparationFailure: unused,
    getKitchenQueue: unused,
    listIncomingOrders: unused,
    listActiveRestaurantOrders: unused,
    registerRestaurant: unused,
    updateRestaurant: unused,
    assignStaff: unused,
    revokeStaff: unused,
    openRestaurant: world.open,
    closeRestaurant: world.close,
    createMenu: unused,
    addMenuItem: unused,
    updateMenuItem: unused,
    setMenuItemAvailability: unused,
    getSimulator: { execute: () => Promise.reject(new Error("unused")) },
    setOpeningHours: unused,
    setScheduledOrderSettings: unused,
    queueRestaurantOrder: unused,
    recordPickup: unused,
    listScheduledSlots: unused,
    reserveScheduledSlot: unused,
    confirmScheduledSlot: unused,
    releaseScheduledSlot: unused,
    listScheduledOrders: unused,
    setSimulatorEnabled: { execute: () => Promise.reject(new Error("unused")) },
    hireCook: { execute: () => Promise.reject(new Error("unused")) },
    putCookOnLine: { execute: () => Promise.reject(new Error("unused")) },
    takeCookOffLine: { execute: () => Promise.reject(new Error("unused")) },
    retireCook: { execute: () => Promise.reject(new Error("unused")) },
    getCookPool: { execute: () => Promise.reject(new Error("unused")) },
    subjects: { resolve: async () => owner },
  });
  const worker = new GrpcWorker(createGrpcServer(service), 0);
  await worker.start();
  const running = worker.run();
  const client = createClient(
    RestaurantsService,
    createGrpcTransport({ baseUrl: `http://127.0.0.1:${worker.port}` }),
  );
  const stop = async () => {
    await worker.shutdown(AbortSignal.timeout(1000));
    await running;
  };
  return { client, restaurantId, metadata, stop };
};

test("AC-1080 an endpoint without the flags exports neither spans nor metrics", async () => {
  const telemetry = startTelemetry(
    loadTelemetryConfig(
      specSchema.parse({ telemetry: { endpoint: "http://127.0.0.1:1" } }),
      { POD_NAME: "restaurants-abc" },
      "restaurants",
    ),
  );
  try {
    assert.equal(telemetry.tracing, false);
    assert.equal(telemetry.enabled, false);
  } finally {
    await telemetry.shutdown(1000);
  }
});

test("AC-1079 spans and metrics go to telemetry.endpoint with the file's service.name, not to OTEL_*", async () => {
  // OTLP/HTTP collector of the file; OTEL_* point to a closed port.
  const received: { path: string; body: Buffer }[] = [];
  const collector = createServer((request, response) => {
    const chunks: Buffer[] = [];
    request.on("data", (chunk: Buffer) => chunks.push(chunk));
    request.on("end", () => {
      received.push({ path: request.url ?? "", body: Buffer.concat(chunks) });
      response.writeHead(200, { "content-type": "application/x-protobuf" });
      response.end();
    });
  });
  await new Promise<void>((resolve) =>
    collector.listen(0, "127.0.0.1", resolve),
  );
  const { port } = collector.address() as AddressInfo;
  const otel = {
    OTEL_SERVICE_NAME: "other-service",
    OTEL_RESOURCE_ATTRIBUTES: "service.name=other-service",
    OTEL_EXPORTER_OTLP_ENDPOINT: "http://127.0.0.1:1",
    OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: "http://127.0.0.1:1/v1/traces",
    OTEL_EXPORTER_OTLP_METRICS_ENDPOINT: "http://127.0.0.1:1/v1/metrics",
    OTEL_EXPORTER_OTLP_PROTOCOL: "grpc",
  };
  Object.assign(process.env, otel);
  try {
    const telemetry = startTelemetry(
      loadTelemetryConfig(
        specSchema.parse({
          telemetry: {
            endpoint: `http://127.0.0.1:${port}`,
            protocol: "http/protobuf",
            serviceName: "restaurants-from-file",
          },
          tracing: { enabled: true },
          metrics: { enabled: true },
        }),
        { ...process.env, POD_NAME: "restaurants-abc" },
        "restaurants",
      ),
    );
    assert.equal(telemetry.tracing, true);
    assert.equal(telemetry.enabled, true);
    assert.equal(telemetry.resource["service.name"], "restaurants-from-file");
    await withSpan(
      "probe",
      { parent: ROOT_CONTEXT, kind: SpanKind.INTERNAL },
      async () => {},
    );
    // Shutdown flushes both providers to the collector.
    await telemetry.shutdown(5000);
  } finally {
    for (const name of Object.keys(otel)) delete process.env[name];
    await new Promise((resolve) => collector.close(resolve));
  }
  assert.deepEqual(
    [...new Set(received.map((request) => request.path))].sort(),
    ["/v1/metrics", "/v1/traces"],
  );
  for (const { body } of received) {
    assert.ok(body.includes("restaurants-from-file"));
    assert.ok(body.includes("restaurants-abc"));
    assert.equal(body.includes("other-service"), false);
  }
});

test("without span export the caller's traceparent reaches the Outbox", async () => {
  installPropagation();
  const { client, restaurantId, metadata, stop } = await startServer();
  try {
    await client.openRestaurant(
      { restaurantId },
      { headers: { traceparent: TRACEPARENT } },
    );
  } finally {
    await stop();
  }
  assert.deepEqual(metadata, [{ traceparent: TRACEPARENT }]);
});

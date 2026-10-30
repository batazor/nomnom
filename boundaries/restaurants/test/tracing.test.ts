import assert from "node:assert/strict";
import test from "node:test";

import { createClient } from "@connectrpc/connect";
import { createGrpcTransport } from "@connectrpc/connect-node";

import { RestaurantsService } from "../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import { createGrpcServer } from "../infrastructure/grpc/server.ts";
import { createRestaurantsService } from "../infrastructure/grpc/service.ts";
import { GrpcWorker } from "../infrastructure/grpc/worker.ts";
import type { Outbox } from "../pkg/outbox/outbox.ts";
import { currentTraceMetadata } from "../pkg/telemetry/trace-context.ts";
import {
  installPropagation,
  tracesEndpointConfigured,
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

test("traces are exported only with an OTLP endpoint", () => {
  assert.equal(tracesEndpointConfigured({}), false);
  assert.equal(
    tracesEndpointConfigured({ OTEL_EXPORTER_OTLP_ENDPOINT: " " }),
    false,
  );
  assert.equal(
    tracesEndpointConfigured({
      OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: "http://alloy:4317",
    }),
    true,
  );
  assert.equal(
    tracesEndpointConfigured({ OTEL_EXPORTER_OTLP_ENDPOINT: "http://a:4317" }),
    true,
  );
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

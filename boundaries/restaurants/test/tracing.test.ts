import assert from "node:assert/strict";
import test from "node:test";

import { createClient } from "@connectrpc/connect";
import { createGrpcTransport } from "@connectrpc/connect-node";
import { SpanKind } from "@opentelemetry/api";
import {
  InMemorySpanExporter,
  SimpleSpanProcessor,
} from "@opentelemetry/sdk-trace";

import { RestaurantsService } from "../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import { createGrpcServer } from "../infrastructure/grpc/server.ts";
import { createRestaurantsService } from "../infrastructure/grpc/service.ts";
import { GrpcWorker } from "../infrastructure/grpc/worker.ts";
import type { Outbox } from "../pkg/application/outbox.ts";
import {
  type ConsumerClient,
  type EachMessage,
  KafkaConsumerWorker,
} from "../pkg/infrastructure/kafka/consumer.ts";
import { currentTraceMetadata } from "../pkg/telemetry/trace-context.ts";
import {
  installPropagation,
  startTracing,
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
 * cmd/restaurants) with an Outbox that records the trace metadata MongoOutbox
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
    listRestaurants: unused,
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
    registerRestaurant: unused,
    assignStaff: unused,
    revokeStaff: unused,
    openRestaurant: world.open,
    closeRestaurant: world.close,
    createMenu: unused,
    addMenuItem: unused,
    updateMenuItem: unused,
    setMenuItemAvailability: unused,
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

test("a gRPC request with traceparent writes an Outbox record in the same trace", async () => {
  const exporter = new InMemorySpanExporter();
  const tracing = startTracing(
    "restaurants",
    {},
    { spanProcessors: [new SimpleSpanProcessor({ exporter })] },
  );
  assert.equal(tracing.enabled, true);
  const { client, restaurantId, metadata, stop } = await startServer();
  try {
    const response = await client.openRestaurant(
      { restaurantId },
      { headers: { traceparent: TRACEPARENT } },
    );
    assert.equal(response.changed, true);
  } finally {
    await stop();
  }

  const [span] = exporter
    .getFinishedSpans()
    .filter(
      (s) =>
        s.name === "nomnom.restaurants.v1.RestaurantsService/OpenRestaurant",
    );
  assert.ok(span, "server span exported");
  assert.equal(span.kind, SpanKind.SERVER);
  assert.equal(span.spanContext().traceId, TRACE_ID);
  assert.equal(span.parentSpanContext?.spanId, CALLER_SPAN_ID);
  assert.equal(span.attributes["rpc.system.name"], "grpc");
  assert.equal(
    span.attributes["rpc.method"],
    "nomnom.restaurants.v1.RestaurantsService/OpenRestaurant",
  );
  assert.equal(span.attributes["rpc.response.status_code"], "OK");

  assert.equal(metadata.length, 1);
  const [version, traceId, spanId, flags] = (
    metadata[0]?.traceparent ?? ""
  ).split("-");
  assert.equal(version, "00");
  assert.equal(traceId, TRACE_ID, "Outbox record continues the caller's trace");
  assert.equal(spanId, span.spanContext().spanId, "parent is the server span");
  assert.equal(flags, "01");

  await tracing.shutdown();
});

test("restaurants-consumer continues the traceparent of a Kafka message", async () => {
  const exporter = new InMemorySpanExporter();
  const tracing = startTracing(
    "restaurants-consumer",
    {},
    { spanProcessors: [new SimpleSpanProcessor({ exporter })] },
  );
  let eachMessage: ((payload: EachMessage) => Promise<void>) | undefined;
  const client: ConsumerClient = {
    connect: async () => {},
    subscribe: async () => {},
    run: async (config) => {
      eachMessage = config.eachMessage;
    },
    disconnect: async () => {},
  };
  const seen: { traceparent?: string }[] = [];
  const worker = new KafkaConsumerWorker({
    client,
    topics: ["orders.v1.order-events"],
    handler: {
      process: async () => {
        seen.push(currentTraceMetadata());
        return "processed";
      },
    },
  });
  await worker.start();
  await eachMessage?.({
    topic: "orders.v1.order-events",
    partition: 0,
    message: {
      key: null,
      value: Buffer.from([]),
      offset: "7",
      headers: { traceparent: Buffer.from(TRACEPARENT) },
    },
  });
  await worker.shutdown(AbortSignal.timeout(1000));

  const [span] = exporter.getFinishedSpans();
  assert.equal(span?.name, "process orders.v1.order-events");
  assert.equal(span?.kind, SpanKind.CONSUMER);
  assert.equal(span?.parentSpanContext?.spanId, CALLER_SPAN_ID);
  assert.equal(
    seen[0]?.traceparent,
    `00-${TRACE_ID}-${span?.spanContext().spanId}-01`,
  );
  await tracing.shutdown();
});

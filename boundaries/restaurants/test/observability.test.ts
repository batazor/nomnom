// Observability contract of the restaurants processes (docs/observability.md):
// trace propagation over gRPC (server and grpc-js client), the Outbox and
// Kafka headers; use case, RPC, MongoDB, Unit of Work, authorization, Kafka
// consumer, runtime and process metrics by name and attributes; JSON logs
// with trace_id; baggage allowlist; bounded flush.
import assert from "node:assert/strict";
import test from "node:test";
import { setTimeout as delay } from "node:timers/promises";
import { setFlagsFromString } from "node:v8";
import { runInNewContext } from "node:vm";

import { v1 } from "@authzed/authzed-node";
import { createClient } from "@connectrpc/connect";
import { createGrpcTransport } from "@connectrpc/connect-node";
import * as grpc from "@grpc/grpc-js";
import {
  context,
  propagation,
  ROOT_CONTEXT,
  SpanKind,
  SpanStatusCode,
  trace,
} from "@opentelemetry/api";
import {
  type DataPoint,
  type Histogram,
  MetricReader,
  type ResourceMetrics,
} from "@opentelemetry/sdk-metrics";
import {
  InMemorySpanExporter,
  type ReadableSpan,
  SimpleSpanProcessor,
  type SpanProcessor,
} from "@opentelemetry/sdk-trace";
import { MongoServerError } from "mongodb";
import { clientTelemetryInterceptor } from "../infrastructure/grpc/client-telemetry.ts";
import { RestaurantsService } from "../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import { createGrpcServer } from "../infrastructure/grpc/server.ts";
import { createRestaurantsService } from "../infrastructure/grpc/service.ts";
import { GrpcWorker } from "../infrastructure/grpc/worker.ts";
import { checkPermissionRequest } from "../infrastructure/spicedb/access-control.ts";
import {
  AUTHZ_DURATION_BUCKETS,
  ObservedPermissionsClient,
} from "../infrastructure/spicedb/check-telemetry.ts";
import { NotFoundError } from "../pkg/application/errors.ts";
import type { Outbox } from "../pkg/application/outbox.ts";
import {
  type ConsumerClient,
  type EachMessage,
  KafkaConsumerWorker,
} from "../pkg/infrastructure/kafka/consumer.ts";
import { DatabaseCommandTelemetry } from "../pkg/infrastructure/repository/command-telemetry.ts";
import {
  DatabaseUnitOfWork,
  UOW_DURATION_BUCKETS,
} from "../pkg/infrastructure/repository/unit-of-work.ts";
import { createLogger, REDACTED } from "../pkg/telemetry/logger.ts";
import { telemetryResource } from "../pkg/telemetry/resource.ts";
import { RPC_DURATION_BUCKETS } from "../pkg/telemetry/rpc.ts";
import { startTelemetry } from "../pkg/telemetry/telemetry.ts";
import { currentTraceMetadata } from "../pkg/telemetry/trace-context.ts";
import {
  AllowlistBaggagePropagator,
  extractContext,
  injectContext,
  withSpan,
} from "../pkg/telemetry/tracing.ts";
import {
  command,
  USECASE_DURATION_BUCKETS,
  UseCaseTelemetry,
} from "../pkg/telemetry/usecase.ts";
import { createTestDatabase } from "./support/database.ts";
import {
  createInfrastructure,
  createRestaurantWorld,
  owner,
} from "./support/restaurants.ts";

const TRACE_ID = "4bf92f3577b34da6a3ce929d0e0e4736";
const CALLER_SPAN_ID = "00f067aa0ba902b7";
const TRACEPARENT = `00-${TRACE_ID}-${CALLER_SPAN_ID}-01`;
const TOPIC = "restaurants.v1.restaurant-events";

class TestReader extends MetricReader {
  protected async onForceFlush(): Promise<void> {}
  protected async onShutdown(): Promise<void> {}
}

/** Telemetry of one test with in-memory span exporter and metric reader. */
const startTestTelemetry = (
  serviceName: string,
  environment: Record<string, string> = {},
) => {
  const exporter = new InMemorySpanExporter();
  const reader = new TestReader();
  const telemetry = startTelemetry(serviceName, environment, {
    spanProcessors: [new SimpleSpanProcessor({ exporter })],
    metricReaders: [reader],
  });
  const collect = async (): Promise<ResourceMetrics> =>
    (await reader.collect()).resourceMetrics;
  return { exporter, telemetry, collect };
};

const metricNamed = (resourceMetrics: ResourceMetrics, name: string) =>
  resourceMetrics.scopeMetrics
    .flatMap((scope) => scope.metrics)
    .find((metric) => metric.descriptor.name === name);

const pointsOf = (resourceMetrics: ResourceMetrics, name: string) =>
  (metricNamed(resourceMetrics, name)?.dataPoints ?? []) as DataPoint<
    Histogram | number
  >[];

const spanNamed = (exporter: InMemorySpanExporter, name: string) => {
  const span = exporter.getFinishedSpans().find((s) => s.name === name);
  assert.ok(span, `span ${name} exported`);
  return span;
};

const unused = {
  execute: async (): Promise<never> => assert.fail("unexpected call"),
};

const parentOf = (span: ReadableSpan) => span.parentSpanContext?.spanId;

test("one trace spans the gRPC call, the use case and, through the Outbox, the Kafka consumer", async () => {
  const { exporter, telemetry, collect } = startTestTelemetry("restaurants");
  try {
    // API: Open Restaurant over HTTP/2 with the caller's traceparent.
    const infrastructure = await createInfrastructure();
    const metadata: { traceparent?: string }[] = [];
    const outbox: Outbox = {
      add: async (aggregate, events) => {
        metadata.push(currentTraceMetadata());
        await infrastructure.outbox.add(aggregate, events);
      },
    };
    const world = await createRestaurantWorld(infrastructure, { outbox });
    const restaurantId = await world.givenRestaurant("Pho Bar", {
      open: false,
    });
    const useCases = new UseCaseTelemetry();
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
      getKitchenQueue: unused,
      listIncomingOrders: unused,
      listActiveRestaurantOrders: unused,
      markRestaurantOrderAssembled: unused,
      markRestaurantOrderReady: unused,
      reportPreparationDelay: unused,
      reportPreparationFailure: unused,
      registerRestaurant: unused,
      updateRestaurant: unused,
      assignStaff: unused,
      revokeStaff: unused,
      openRestaurant: useCases.instrument(
        command("open_restaurant"),
        world.open,
      ),
      closeRestaurant: world.close,
      createMenu: unused,
      addMenuItem: unused,
      updateMenuItem: unused,
      setMenuItemAvailability: unused,
      getSimulator: { execute: () => Promise.reject(new Error("unused")) },
      setSimulatorEnabled: {
        execute: () => Promise.reject(new Error("unused")),
      },
      hireCook: {
        execute: () => Promise.reject(new Error("unused")),
      },
      putCookOnLine: {
        execute: () => Promise.reject(new Error("unused")),
      },
      takeCookOffLine: {
        execute: () => Promise.reject(new Error("unused")),
      },
      retireCook: {
        execute: () => Promise.reject(new Error("unused")),
      },
      getCookPool: {
        execute: () => Promise.reject(new Error("unused")),
      },
      subjects: { resolve: async () => owner },
    });
    const worker = new GrpcWorker(createGrpcServer(service), 0);
    await worker.start();
    const running = worker.run();
    try {
      const client = createClient(
        RestaurantsService,
        createGrpcTransport({ baseUrl: `http://127.0.0.1:${worker.port}` }),
      );
      await client.openRestaurant(
        { restaurantId },
        { headers: { traceparent: TRACEPARENT } },
      );
    } finally {
      await worker.shutdown(AbortSignal.timeout(1000));
      await running;
    }

    const method = "nomnom.restaurants.v1.RestaurantsService/OpenRestaurant";
    const server = spanNamed(exporter, method);
    const useCase = spanNamed(exporter, "usecase open_restaurant");
    assert.equal(server.kind, SpanKind.SERVER);
    assert.equal(parentOf(server), CALLER_SPAN_ID);
    assert.equal(parentOf(useCase), server.spanContext().spanId);
    assert.deepEqual(useCase.attributes, {
      "nomnom.usecase.name": "open_restaurant",
      "nomnom.usecase.kind": "command",
      "nomnom.outcome": "success",
    });
    // The Outbox record carries the context of the use case span.
    assert.equal(
      metadata[0]?.traceparent,
      `00-${TRACE_ID}-${useCase.spanContext().spanId}-01`,
    );

    // Debezium (ADR 37) copies the Outbox traceparent into the Kafka headers
    // as is: no PRODUCER span between the use case and the consumer.
    const headers: Record<string, string> = {
      event_id: "e-1",
      event_type: "nomnom.restaurants.v1.RestaurantOpened",
      traceparent: metadata[0]?.traceparent ?? "",
    };

    // Consumer: the process span is the child of the use case span.
    let eachMessage: ((payload: EachMessage) => Promise<void>) | undefined;
    const kafka: ConsumerClient = {
      connect: async () => {},
      subscribe: async () => {},
      run: async (config) => {
        eachMessage = config.eachMessage;
      },
      disconnect: async () => {},
    };
    const consumer = new KafkaConsumerWorker({
      client: kafka,
      topics: [TOPIC],
      groupId: "delivery",
      handler: { process: async () => "processed" },
    });
    await consumer.start();
    await eachMessage?.({
      topic: TOPIC,
      partition: 3,
      message: {
        key: Buffer.from(restaurantId),
        value: Buffer.from([]),
        offset: "42",
        headers: Object.fromEntries(
          Object.entries(headers).map(([k, v]) => [k, Buffer.from(v)]),
        ),
      },
    });
    await consumer.shutdown(AbortSignal.timeout(1000));
    const process = spanNamed(exporter, `process ${TOPIC}`);
    assert.equal(process.kind, SpanKind.CONSUMER);
    assert.equal(process.spanContext().traceId, TRACE_ID);
    assert.equal(parentOf(process), useCase.spanContext().spanId);
    assert.equal(
      process.attributes["messaging.consumer.group.name"],
      "delivery",
    );
    assert.equal(process.attributes["messaging.message.id"], "e-1");

    // Metrics: rpc.server.call.duration and nomnom.usecase.duration.
    const metrics = await collect();
    const rpc = metricNamed(metrics, "rpc.server.call.duration");
    assert.equal(rpc?.descriptor.unit, "s");
    const [rpcPoint] = pointsOf(metrics, "rpc.server.call.duration");
    assert.ok(rpcPoint);
    assert.deepEqual(rpcPoint.attributes, {
      "rpc.system.name": "grpc",
      "rpc.method": method,
      "rpc.response.status_code": "OK",
    });
    assert.deepEqual(
      (rpcPoint.value as Histogram).buckets.boundaries,
      RPC_DURATION_BUCKETS,
    );
    const [useCasePoint] = pointsOf(metrics, "nomnom.usecase.duration");
    assert.ok(useCasePoint);
    assert.deepEqual(useCasePoint.attributes, {
      "nomnom.usecase.name": "open_restaurant",
      "nomnom.usecase.kind": "command",
      "nomnom.outcome": "success",
    });
    assert.deepEqual(
      (useCasePoint.value as Histogram).buckets.boundaries,
      USECASE_DURATION_BUCKETS,
    );
  } finally {
    await telemetry.shutdown();
  }
});

test("use case outcomes: rejected and error carry a closed error.type", async () => {
  const { exporter, telemetry, collect } = startTestTelemetry("restaurants");
  try {
    const useCases = new UseCaseTelemetry();
    const failing = (error: Error) =>
      useCases.instrument(command("accept_order"), {
        execute: async (_input: unknown): Promise<never> => {
          throw error;
        },
      });
    const cancel = new Error("aborted");
    cancel.name = "AbortError";
    const failures = [
      new NotFoundError("restaurant order", "o-1"),
      new Error("connection to mongodb://u:secret@db lost"),
      cancel,
    ];
    for (const error of failures) {
      await assert.rejects(failing(error).execute(undefined as never), error);
    }

    const outcomes = exporter
      .getFinishedSpans()
      .map((span) => [
        span.attributes["nomnom.outcome"],
        span.attributes["error.type"],
        span.status.code,
        span.status.message,
      ]);
    assert.deepEqual(outcomes, [
      ["rejected", "not_found", SpanStatusCode.UNSET, undefined],
      ["error", "internal", SpanStatusCode.ERROR, "internal"],
      ["error", "canceled", SpanStatusCode.ERROR, "canceled"],
    ]);
    for (const span of exporter.getFinishedSpans()) {
      assert.equal(span.events.length, 0, "no exception events with text");
    }
    const labels = pointsOf(await collect(), "nomnom.usecase.duration").map(
      (point) => point.attributes,
    );
    assert.deepEqual(labels, [
      {
        "nomnom.usecase.name": "accept_order",
        "nomnom.usecase.kind": "command",
        "nomnom.outcome": "rejected",
        "error.type": "not_found",
      },
      {
        "nomnom.usecase.name": "accept_order",
        "nomnom.usecase.kind": "command",
        "nomnom.outcome": "error",
        "error.type": "internal",
      },
      {
        "nomnom.usecase.name": "accept_order",
        "nomnom.usecase.kind": "command",
        "nomnom.outcome": "error",
        "error.type": "canceled",
      },
    ]);
  } finally {
    await telemetry.shutdown();
  }
});

/** grpc-js server of one unary method that echoes the request metadata. */
const startEchoServer = async (code: grpc.status) => {
  const seen: grpc.Metadata[] = [];
  const server = new grpc.Server();
  const identity = (value: Buffer) => value;
  server.addService(
    {
      check: {
        path: "/authzed.api.v1.PermissionsService/CheckPermission",
        requestStream: false,
        responseStream: false,
        requestSerialize: identity,
        requestDeserialize: identity,
        responseSerialize: identity,
        responseDeserialize: identity,
      },
    },
    {
      check: (
        call: grpc.ServerUnaryCall<Buffer, Buffer>,
        callback: grpc.sendUnaryData<Buffer>,
      ) => {
        seen.push(call.metadata);
        if (code === grpc.status.OK) callback(null, Buffer.from("ok"));
        else callback({ code, details: "no" }, null);
      },
    },
  );
  const port = await new Promise<number>((resolve, reject) =>
    server.bindAsync(
      "127.0.0.1:0",
      grpc.ServerCredentials.createInsecure(),
      (error, bound) => (error ? reject(error) : resolve(bound)),
    ),
  );
  return { port, seen, stop: () => server.forceShutdown() };
};

test("the grpc-js client (SpiceDB) propagates the trace and records rpc.client.call.duration", async () => {
  const { exporter, telemetry, collect } = startTestTelemetry("restaurants");
  const ok = await startEchoServer(grpc.status.OK);
  const denied = await startEchoServer(grpc.status.PERMISSION_DENIED);
  try {
    const call = (port: number) => {
      const client = new grpc.Client(
        `127.0.0.1:${port}`,
        grpc.credentials.createInsecure(),
        {
          interceptors: [
            clientTelemetryInterceptor({ address: "spicedb", port: 50051 }),
          ],
        },
      );
      return new Promise<void>((resolve) =>
        client.makeUnaryRequest(
          "/authzed.api.v1.PermissionsService/CheckPermission",
          (value: Buffer) => value,
          (value: Buffer) => value,
          Buffer.from("request"),
          () => {
            client.close();
            resolve();
          },
        ),
      );
    };
    await context.with(extractContext({ traceparent: TRACEPARENT }), () =>
      call(ok.port),
    );
    await call(denied.port);

    const method = "authzed.api.v1.PermissionsService/CheckPermission";
    const [first, second] = exporter
      .getFinishedSpans()
      .filter((span) => span.name === method);
    assert.ok(first && second);
    assert.equal(first.kind, SpanKind.CLIENT);
    assert.equal(first.spanContext().traceId, TRACE_ID);
    assert.equal(parentOf(first), CALLER_SPAN_ID);
    assert.equal(
      ok.seen[0]?.get("traceparent")[0],
      `00-${TRACE_ID}-${first.spanContext().spanId}-01`,
      "the server receives the client span as parent",
    );
    assert.equal(first.attributes["rpc.response.status_code"], "OK");
    assert.equal(second.attributes["error.type"], "PERMISSION_DENIED");
    assert.equal(second.status.code, SpanStatusCode.ERROR);

    const labels = pointsOf(await collect(), "rpc.client.call.duration").map(
      (point) => point.attributes,
    );
    assert.deepEqual(labels, [
      {
        "rpc.system.name": "grpc",
        "rpc.method": method,
        "rpc.response.status_code": "OK",
        "server.address": "spicedb",
        "server.port": 50051,
      },
      {
        "rpc.system.name": "grpc",
        "rpc.method": method,
        "rpc.response.status_code": "PERMISSION_DENIED",
        "server.address": "spicedb",
        "server.port": 50051,
        "error.type": "PERMISSION_DENIED",
      },
    ]);
  } finally {
    ok.stop();
    denied.stop();
    await telemetry.shutdown();
  }
});

test("MongoDB commands become client spans and db.client.operation.duration without payload", async () => {
  const { exporter, telemetry, collect } = startTestTelemetry("restaurants");
  try {
    const database = new DatabaseCommandTelemetry();
    const base = {
      databaseName: "restaurants",
      address: "mongodb-0:27017",
      connectionId: 3,
    };
    await withSpan(
      "usecase get_restaurant",
      { parent: ROOT_CONTEXT, kind: SpanKind.INTERNAL },
      async () => {
        database.started({
          ...base,
          requestId: 1,
          commandName: "find",
          command: { find: "restaurants", filter: { owner: "secret@x" } },
        } as never);
        database.succeeded({ requestId: 1, connectionId: 3 } as never);
        database.started({
          ...base,
          requestId: 2,
          commandName: "insert",
          command: { insert: "outbox", documents: [{ payload: "x" }] },
        } as never);
        database.failed({
          requestId: 2,
          connectionId: 3,
          failure: Object.assign(new Error("dup"), {
            codeName: "DuplicateKey",
          }),
        } as never);
        database.started({
          ...base,
          requestId: 3,
          commandName: "hello",
          command: { hello: 1 },
        } as never);
      },
    );
    const parent = spanNamed(exporter, "usecase get_restaurant");
    const find = spanNamed(exporter, "find restaurants");
    const insert = spanNamed(exporter, "insert outbox");
    assert.equal(find.kind, SpanKind.CLIENT);
    assert.equal(parentOf(find), parent.spanContext().spanId);
    assert.deepEqual(find.attributes, {
      "db.system.name": "mongodb",
      "db.operation.name": "find",
      "db.collection.name": "restaurants",
      "db.namespace": "restaurants",
      "server.address": "mongodb-0",
      "server.port": 27017,
    });
    assert.equal(insert.attributes["error.type"], "DuplicateKey");
    assert.equal(insert.status.code, SpanStatusCode.ERROR);
    assert.equal(exporter.getFinishedSpans().length, 3, "hello is ignored");

    const labels = pointsOf(
      await collect(),
      "db.client.operation.duration",
    ).map((point) => point.attributes);
    assert.deepEqual(labels, [
      {
        "db.system.name": "mongodb",
        "db.operation.name": "find",
        "db.collection.name": "restaurants",
      },
      {
        "db.system.name": "mongodb",
        "db.operation.name": "insert",
        "db.collection.name": "outbox",
        "error.type": "DuplicateKey",
      },
    ]);
  } finally {
    await telemetry.shutdown();
  }
});

test("MongoDB commands outside a trace record the metric but open no root span", async () => {
  const { exporter, telemetry, collect } = startTestTelemetry("restaurants");
  try {
    const database = new DatabaseCommandTelemetry();
    // The reconciler loop runs outside any trace.
    database.started(
      {
        databaseName: "restaurants",
        address: "mongodb-0:27017",
        connectionId: 1,
        requestId: 7,
        commandName: "find",
        command: { find: "restaurants" },
      } as never,
      ROOT_CONTEXT,
    );
    database.succeeded({ requestId: 7, connectionId: 1 } as never);
    assert.equal(exporter.getFinishedSpans().length, 0);
    const labels = pointsOf(
      await collect(),
      "db.client.operation.duration",
    ).map((point) => point.attributes);
    assert.deepEqual(labels, [
      {
        "db.system.name": "mongodb",
        "db.operation.name": "find",
        "db.collection.name": "restaurants",
      },
    ]);
  } finally {
    await telemetry.shutdown();
  }
});

test("runtime and process metrics share the resource of the traces", async () => {
  const { exporter, telemetry, collect } = startTestTelemetry("restaurants", {
    OTEL_RESOURCE_ATTRIBUTES:
      "service.version=1.2.3,deployment.environment.name=local,service.instance.id=restaurants-abc",
  });
  try {
    // Event loop delay is reported after five samples; force one GC.
    await delay(150);
    setFlagsFromString("--expose-gc");
    (runInNewContext("gc") as () => void)();
    await delay(10);
    await withSpan(
      "probe",
      { parent: ROOT_CONTEXT, kind: SpanKind.INTERNAL },
      async () => {},
    );
    const metrics = await collect();
    for (const name of [
      "v8js.memory.heap.used",
      "v8js.memory.heap.limit",
      "nodejs.eventloop.delay.p99",
      "v8js.gc.duration",
      "process.cpu.time",
      "process.memory.usage",
    ]) {
      assert.ok(pointsOf(metrics, name).length > 0, `${name} is collected`);
    }
    assert.ok(
      pointsOf(metrics, "v8js.memory.heap.used").every(
        (point) => typeof point.attributes["v8js.heap.space.name"] === "string",
      ),
    );
    assert.deepEqual(
      pointsOf(metrics, "process.cpu.time")
        .map((point) => point.attributes["cpu.mode"])
        .sort(),
      ["system", "user"],
    );
    const expected = {
      "service.name": "restaurants",
      "service.namespace": "nomnom",
      "service.version": "1.2.3",
      "deployment.environment.name": "local",
      "service.instance.id": "restaurants-abc",
    };
    const [span] = exporter.getFinishedSpans();
    for (const [key, value] of Object.entries(expected)) {
      assert.equal(metrics.resource.attributes[key], value, `metrics ${key}`);
      assert.equal(span?.resource.attributes[key], value, `traces ${key}`);
    }
  } finally {
    await telemetry.shutdown();
  }
});

test("logs are one JSON line with the service identity and trace_id only inside a span", async () => {
  const { telemetry } = startTestTelemetry("restaurants");
  try {
    const lines: string[] = [];
    const logger = createLogger({
      resource: telemetryResource("restaurants-consumer", {
        OTEL_RESOURCE_ATTRIBUTES:
          "service.version=1.2.3,deployment.environment.name=local",
      }),
      write: (line) => lines.push(line),
      now: () => new Date("2026-10-25T12:00:00+08:00"),
    });
    const consumer = logger.child({ operation: "consumer" });
    consumer.info("partitions assigned");
    let spanContext = trace
      .wrapSpanContext({
        traceId: "",
        spanId: "",
        traceFlags: 0,
      })
      .spanContext();
    await withSpan(
      "send x",
      { parent: ROOT_CONTEXT, kind: SpanKind.PRODUCER },
      async (span) => {
        spanContext = span.spanContext();
        consumer.error("dead letter failed", {
          "messaging.destination.name": "restaurants.v1.menu-events",
          token: "abc",
          uri: "mongodb://restaurants:pa55@mongo:27017",
          error: new Error("Bearer eyJabc.def failed"),
        });
      },
    );

    assert.equal(lines.length, 2);
    for (const line of lines) assert.equal(line.includes("\n"), false);
    const [outside, inside] = lines.map(
      (line) => JSON.parse(line) as Record<string, unknown>,
    );
    assert.deepEqual(outside, {
      timestamp: "2026-10-25T04:00:00.000Z",
      severity: "INFO",
      message: "partitions assigned",
      "service.namespace": "nomnom",
      "service.name": "restaurants-consumer",
      "service.version": "1.2.3",
      "deployment.environment.name": "local",
      operation: "consumer",
    });
    assert.equal(inside?.severity, "ERROR");
    assert.equal(inside?.trace_id, spanContext.traceId);
    assert.equal(inside?.span_id, spanContext.spanId);
    assert.equal(inside?.operation, "consumer");
    assert.equal(inside?.token, REDACTED);
    assert.equal(inside?.uri, `mongodb://restaurants:${REDACTED}@mongo:27017`);
    assert.equal(inside?.["error.type"], "Error");
    assert.equal(inside?.["error.message"], `Bearer ${REDACTED} failed`);
    const stack = inside?.["error.stack"];
    assert.ok(Array.isArray(stack));
    assert.match(String(stack[0]), /^at /);
  } finally {
    await telemetry.shutdown();
  }
});

test("Unit of Work transactions, duration and driver retries by outcome and reason", async () => {
  const { telemetry, collect } = startTestTelemetry("restaurants");
  try {
    const { database, db } = await createTestDatabase();
    const unitOfWork = new DatabaseUnitOfWork(database.client);
    const notes = db.collection<{ _id: string }>("uow_notes");
    const note = (id: string) =>
      notes.insertOne({ _id: id }, { session: unitOfWork.session });

    await unitOfWork.run(async () => {
      // A nested run joins the transaction and is not counted.
      await unitOfWork.run(() => note("committed"));
    });
    await assert.rejects(
      unitOfWork.run(async () => {
        await note("rolled-back");
        throw new Error("abort");
      }),
      /abort/,
    );
    // The driver retries `work` after a transient write conflict.
    let attempts = 0;
    await unitOfWork.run(async () => {
      attempts += 1;
      await note(`retried-${attempts}`);
      if (attempts === 1) {
        throw new MongoServerError({
          code: 112,
          codeName: "WriteConflict",
          errmsg: "write conflict",
          errorLabels: ["TransientTransactionError"],
        });
      }
    });
    assert.equal(attempts, 2);
    assert.deepEqual(
      (await notes.find().toArray()).map((doc) => doc._id).sort(),
      ["committed", "retried-2"],
    );

    const metrics = await collect();
    const sums = (name: string, label: string) =>
      Object.fromEntries(
        pointsOf(metrics, name).map((point) => [
          point.attributes[label],
          point.value,
        ]),
      );
    assert.equal(
      metricNamed(metrics, "nomnom.uow.duration")?.descriptor.unit,
      "s",
    );
    assert.deepEqual(sums("nomnom.uow.transactions", "nomnom.outcome"), {
      committed: 2,
      rolled_back: 1,
    });
    assert.deepEqual(sums("nomnom.uow.retries", "nomnom.reason"), {
      write_conflict: 1,
    });
    const durations = pointsOf(metrics, "nomnom.uow.duration");
    assert.deepEqual(
      durations.map((point) => [
        point.attributes["nomnom.outcome"],
        (point.value as Histogram).count,
      ]),
      [
        ["committed", 2],
        ["rolled_back", 1],
      ],
    );
    const [committed] = durations;
    assert.ok(committed);
    assert.deepEqual(
      (committed.value as Histogram).buckets.boundaries,
      UOW_DURATION_BUCKETS,
    );
  } finally {
    await telemetry.shutdown();
  }
});

test("SpiceDB checks record nomnom.authz.checks and duration by permission, resource type and result", async () => {
  const { telemetry, collect } = startTestTelemetry("restaurants");
  try {
    const responses: Array<() => Promise<v1.CheckPermissionResponse>> = [
      async () =>
        v1.CheckPermissionResponse.create({
          permissionship:
            v1.CheckPermissionResponse_Permissionship.HAS_PERMISSION,
        }),
      async () =>
        v1.CheckPermissionResponse.create({
          permissionship:
            v1.CheckPermissionResponse_Permissionship.CONDITIONAL_PERMISSION,
        }),
      async () => {
        throw Object.assign(new Error("deadline"), {
          code: grpc.status.DEADLINE_EXCEEDED,
        });
      },
      async () => {
        throw Object.assign(new Error("unavailable"), {
          code: grpc.status.UNAVAILABLE,
        });
      },
    ];
    const client = new ObservedPermissionsClient({
      checkPermission: () => {
        const next = responses.shift();
        assert.ok(next, "unexpected check");
        return next();
      },
    });
    const subject = { type: "user", id: "u-secret" } as const;
    const check = (permission: string) =>
      client.checkPermission(
        checkPermissionRequest("restaurant", "r-secret", permission, subject),
      );

    await check("manage");
    await check("manage");
    await assert.rejects(check("operate_kitchen"), /deadline/);
    await assert.rejects(
      client.checkPermission(
        checkPermissionRequest(
          "platform",
          "nomnom",
          "register_restaurant",
          subject,
        ),
      ),
      /unavailable/,
    );

    const metrics = await collect();
    const labels = (name: string) =>
      pointsOf(metrics, name).map((point) => [
        point.attributes,
        name === "nomnom.authz.checks"
          ? point.value
          : (point.value as Histogram).count,
      ]);
    const expected = [
      [
        {
          "nomnom.authz.permission": "manage",
          "nomnom.authz.resource_type": "restaurant",
          "nomnom.authz.result": "allowed",
        },
        1,
      ],
      [
        {
          "nomnom.authz.permission": "manage",
          "nomnom.authz.resource_type": "restaurant",
          "nomnom.authz.result": "denied",
        },
        1,
      ],
      [
        {
          "nomnom.authz.permission": "operate_kitchen",
          "nomnom.authz.resource_type": "restaurant",
          "nomnom.authz.result": "timeout",
        },
        1,
      ],
      [
        {
          "nomnom.authz.permission": "register_restaurant",
          "nomnom.authz.resource_type": "platform",
          "nomnom.authz.result": "error",
        },
        1,
      ],
    ];
    assert.deepEqual(labels("nomnom.authz.checks"), expected);
    assert.deepEqual(labels("nomnom.authz.check.duration"), expected);
    const [duration] = pointsOf(metrics, "nomnom.authz.check.duration");
    assert.ok(duration);
    assert.deepEqual(
      (duration.value as Histogram).buckets.boundaries,
      AUTHZ_DURATION_BUCKETS,
    );
  } finally {
    await telemetry.shutdown();
  }
});

test("the Kafka consumer counts messaging.client.consumed.messages with error.type on failure", async () => {
  const { telemetry, collect } = startTestTelemetry("restaurants-consumer");
  try {
    let eachMessage: ((payload: EachMessage) => Promise<void>) | undefined;
    const kafka: ConsumerClient = {
      connect: async () => {},
      subscribe: async () => {},
      run: async (config) => {
        eachMessage = config.eachMessage;
      },
      disconnect: async () => {},
    };
    const outcomes: Array<() => Promise<"processed">> = [
      async () => "processed",
      async () => {
        throw new TypeError("transient after retries");
      },
    ];
    const consumer = new KafkaConsumerWorker({
      client: kafka,
      topics: [TOPIC],
      groupId: "restaurants.order-placed",
      handler: {
        process: () => {
          const next = outcomes.shift();
          assert.ok(next, "unexpected message");
          return next();
        },
      },
    });
    await consumer.start();
    const deliver = (offset: string) =>
      eachMessage?.({
        topic: TOPIC,
        partition: 0,
        message: { key: null, value: Buffer.from([]), offset, headers: {} },
      });
    await deliver("1");
    await assert.rejects(Promise.resolve(deliver("2")), TypeError);
    await consumer.shutdown(AbortSignal.timeout(1000));

    const metrics = await collect();
    assert.equal(
      metricNamed(metrics, "messaging.client.consumed.messages")?.descriptor
        .unit,
      "{message}",
    );
    const base = {
      "messaging.system": "kafka",
      "messaging.destination.name": TOPIC,
      "messaging.consumer.group.name": "restaurants.order-placed",
    };
    assert.deepEqual(
      pointsOf(metrics, "messaging.client.consumed.messages").map((point) => [
        point.attributes,
        point.value,
      ]),
      [
        [base, 1],
        [{ ...base, "error.type": "TypeError" }, 1],
      ],
    );
  } finally {
    await telemetry.shutdown();
  }
});

// Its shutdown is not awaited, so the tests that read metrics run before it.
test("baggage passes only by allowlist and an invalid traceparent starts a new trace", () => {
  const { telemetry } = startTestTelemetry("restaurants");
  try {
    const incoming = {
      traceparent: "00-zzzz-yyyy-01",
      baggage: "user.email=a%40b.c,tenant=t-1",
    };
    const ctx = extractContext(incoming);
    assert.equal(trace.getSpanContext(ctx), undefined);
    const outgoing: Record<string, string> = {};
    injectContext(ctx, outgoing);
    assert.deepEqual(outgoing, {}, "no baggage key is allowed by default");

    const allowlisted = new AllowlistBaggagePropagator(["tenant"]);
    const getter = {
      keys: (carrier: Record<string, string>) => Object.keys(carrier),
      get: (carrier: Record<string, string>, key: string) => carrier[key],
    };
    const filtered = allowlisted.extract(ROOT_CONTEXT, incoming, getter);
    assert.deepEqual(
      propagation
        .getBaggage(filtered)
        ?.getAllEntries()
        .map(([key]) => key),
      ["tenant"],
    );
  } finally {
    void telemetry.shutdown();
  }
});

test("telemetry shutdown is bounded by the flush timeout", async () => {
  const hanging: SpanProcessor = {
    onStart: () => {},
    onEnd: () => {},
    forceFlush: () => new Promise(() => {}),
    shutdown: () => new Promise(() => {}),
  };
  const telemetry = startTelemetry(
    "restaurants",
    {},
    { spanProcessors: [hanging], metricReaders: [new TestReader()] },
  );
  const started = performance.now();
  await telemetry.shutdown(50);
  assert.ok(performance.now() - started < 1000);
  // The hanging provider stays registered; release the global API.
  trace.disable();
});

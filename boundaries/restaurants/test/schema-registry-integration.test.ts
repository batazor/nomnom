import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { after, before, describe, test } from "node:test";

import {
  GenericContainer,
  type StartedTestContainer,
  Wait,
} from "testcontainers";

import {
  HttpSchemaRegistry,
  UnknownSchemaError,
} from "../pkg/infrastructure/messaging/schema-registry.ts";

/** Broker of ops/kind/manifests/messaging/infrastructure.yaml, with its Schema Registry. */
const REDPANDA_IMAGE =
  "docker.io/redpandadata/redpanda:v26.2.6@sha256:8b1076e0d61bfceb66594dc01536427ce0b529f8ead35fdab9763ab6085891ce";

const DELIVERY_EVENTS = new URL(
  "../../delivery/infrastructure/grpc/nomnom/delivery/v1/events.proto",
  import.meta.url,
);

describe("HttpSchemaRegistry against the Redpanda Schema Registry", () => {
  let redpanda: StartedTestContainer;
  let stopped = false;
  let url: URL;
  let schemaId: number;

  before(async () => {
    redpanda = await new GenericContainer(REDPANDA_IMAGE)
      .withCommand([
        "redpanda",
        "start",
        "--mode=dev-container",
        "--smp=1",
        "--memory=256M",
        "--overprovisioned",
        "--schema-registry-addr=0.0.0.0:8081",
      ])
      .withExposedPorts(8081)
      .withWaitStrategy(Wait.forHttp("/subjects", 8081))
      .withStartupTimeout(120_000)
      .start();
    url = new URL(`http://127.0.0.1:${redpanda.getMappedPort(8081)}/`);
    const schema = await readFile(DELIVERY_EVENTS, "utf8");
    for (const subject of [
      "nomnom.delivery.v1.DeliveryPickedUp",
      "nomnom.delivery.v1.DeliveryCompleted",
    ]) {
      const response = await fetch(
        new URL(`subjects/${subject}/versions`, url),
        {
          method: "POST",
          headers: { "Content-Type": "application/vnd.schemaregistry.v1+json" },
          body: JSON.stringify({ schemaType: "PROTOBUF", schema }),
        },
      );
      assert.equal(response.status, 200, await response.clone().text());
      schemaId = ((await response.json()) as { id: number }).id;
    }
  });

  after(async () => {
    if (!stopped) await redpanda?.stop();
  });

  test("FR-717: the subjects of a schema id are the events of its file", async () => {
    const registry = new HttpSchemaRegistry({ url, timeoutMs: 5_000 });

    assert.deepEqual([...(await registry.subjects(schemaId))].sort(), [
      "nomnom.delivery.v1.DeliveryCompleted",
      "nomnom.delivery.v1.DeliveryPickedUp",
    ]);
  });

  test("AC-978: an unknown schema id is UnknownSchemaError", async () => {
    const registry = new HttpSchemaRegistry({ url, timeoutMs: 5_000 });

    await assert.rejects(registry.subjects(schemaId + 100), UnknownSchemaError);
  });

  test("AC-979: an unavailable Registry is a transient error, a known id stays cached", async () => {
    const registry = new HttpSchemaRegistry({ url, timeoutMs: 5_000 });
    await registry.subjects(schemaId);
    const unavailable = new HttpSchemaRegistry({
      url: new URL("http://127.0.0.1:1/"),
      timeoutMs: 1_000,
    });

    await assert.rejects(unavailable.subjects(schemaId), (error: unknown) => {
      assert.ok(!(error instanceof UnknownSchemaError));
      return true;
    });
    await redpanda.stop();
    stopped = true;
    assert.equal((await registry.subjects(schemaId)).length, 2);
  });
});

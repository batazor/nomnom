import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { after, before, describe, test } from "node:test";

import {
  GenericContainer,
  Network,
  type StartedNetwork,
  type StartedTestContainer,
  Wait,
} from "testcontainers";

import {
  HttpSchemaRegistry,
  UnknownSchemaError,
} from "../infrastructure/messaging/schema-registry.ts";

/** Kafka and Confluent Schema Registry of ops/kind/manifests/messaging (kafka.yaml, schema-registry.yaml). */
const KAFKA_IMAGE = "apache/kafka:4.3.1";
const SCHEMA_REGISTRY_IMAGE =
  "docker.io/confluentinc/cp-schema-registry:8.4.0@sha256:12cd2770fa25df65243dabafd5eef5337ea1145a3cb85afc8a27f7151dfa2999";

const DELIVERY_EVENTS = new URL(
  "../../delivery/infrastructure/grpc/nomnom/delivery/v1/events.proto",
  import.meta.url,
);

describe("HttpSchemaRegistry against Confluent Schema Registry", () => {
  let network: StartedNetwork;
  let kafka: StartedTestContainer;
  let registryContainer: StartedTestContainer;
  let stopped = false;
  let url: URL;
  let schemaId: number;

  before(async () => {
    network = await new Network().start();
    kafka = await new GenericContainer(KAFKA_IMAGE)
      .withNetwork(network)
      .withNetworkAliases("kafka")
      .withEnvironment({
        KAFKA_NODE_ID: "1",
        KAFKA_PROCESS_ROLES: "broker,controller",
        KAFKA_LISTENERS: "PLAINTEXT://:9092,CONTROLLER://:9093",
        KAFKA_ADVERTISED_LISTENERS: "PLAINTEXT://kafka:9092",
        KAFKA_CONTROLLER_LISTENER_NAMES: "CONTROLLER",
        KAFKA_LISTENER_SECURITY_PROTOCOL_MAP:
          "CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT",
        KAFKA_CONTROLLER_QUORUM_VOTERS: "1@localhost:9093",
        KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR: "1",
        KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR: "1",
        KAFKA_TRANSACTION_STATE_LOG_MIN_ISR: "1",
      })
      .withWaitStrategy(Wait.forLogMessage(/Kafka Server started/))
      .withStartupTimeout(180_000)
      .start();
    registryContainer = await new GenericContainer(SCHEMA_REGISTRY_IMAGE)
      .withNetwork(network)
      .withEnvironment({
        SCHEMA_REGISTRY_HOST_NAME: "schema-registry",
        SCHEMA_REGISTRY_LISTENERS: "http://0.0.0.0:8081",
        SCHEMA_REGISTRY_KAFKASTORE_BOOTSTRAP_SERVERS: "PLAINTEXT://kafka:9092",
      })
      .withExposedPorts(8081)
      .withWaitStrategy(Wait.forHttp("/subjects", 8081))
      .withStartupTimeout(180_000)
      .start();
    url = new URL(`http://127.0.0.1:${registryContainer.getMappedPort(8081)}/`);
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
    if (!stopped) await registryContainer?.stop();
    await kafka?.stop();
    await network?.stop();
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
    await registryContainer.stop();
    stopped = true;
    assert.equal((await registry.subjects(schemaId)).length, 2);
  });
});

import { createServer } from "node:net";
import { fileURLToPath } from "node:url";

import {
  MongoDBContainer,
  type StartedMongoDBContainer,
} from "@testcontainers/mongodb";
import {
  GenericContainer,
  type StartedTestContainer,
  Wait,
} from "testcontainers";

/** Images of the kind environment (ops/kind/bootstrap/values.yaml, ops/kind/manifests/messaging). */
const MONGODB_IMAGE = "mongo:8.3.13";
const KAFKA_IMAGE = "apache/kafka:4.4.0";
const SPICEDB_IMAGE = "authzed/spicedb:v1.57.0";
const VALKEY_IMAGE = "valkey/valkey:9.1.2-alpine3.24";

/** Preshared key of the test SpiceDB (same as `make spicedb-dev`). */
export const SPICEDB_PRESHARED_KEY = "devkey";

const AUTHORIZATION_DIR = fileURLToPath(
  new URL("../../../../authorization", import.meta.url),
);

const started: StartedTestContainer[] = [];

/** A free host port: Kafka must advertise the same port the host dials. */
async function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.once("error", reject);
    server.listen(0, () => {
      const address = server.address();
      server.close(() => {
        if (address === null || typeof address === "string") {
          reject(new Error("no TCP port allocated"));
        } else {
          resolve(address.port);
        }
      });
    });
  });
}

async function startMongo(): Promise<StartedMongoDBContainer> {
  return new MongoDBContainer(MONGODB_IMAGE).start();
}

async function startKafka(port: number): Promise<StartedTestContainer> {
  return new GenericContainer(KAFKA_IMAGE)
    .withExposedPorts({ container: port, host: port })
    .withEnvironment({
      KAFKA_NODE_ID: "1",
      KAFKA_PROCESS_ROLES: "broker,controller",
      KAFKA_LISTENERS: `PLAINTEXT://0.0.0.0:${port},CONTROLLER://localhost:9093`,
      KAFKA_ADVERTISED_LISTENERS: `PLAINTEXT://localhost:${port}`,
      KAFKA_CONTROLLER_LISTENER_NAMES: "CONTROLLER",
      KAFKA_LISTENER_SECURITY_PROTOCOL_MAP:
        "CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT",
      KAFKA_CONTROLLER_QUORUM_VOTERS: "1@localhost:9093",
      KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR: "1",
      KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR: "1",
      KAFKA_TRANSACTION_STATE_LOG_MIN_ISR: "1",
      KAFKA_GROUP_INITIAL_REBALANCE_DELAY_MS: "0",
    })
    .withWaitStrategy(Wait.forLogMessage(/Kafka Server started/))
    .withStartupTimeout(180_000)
    .start();
}

/** SpiceDB with the project's schema and grants (authorization/bootstrap.yaml). */
async function startSpiceDb(): Promise<StartedTestContainer> {
  return new GenericContainer(SPICEDB_IMAGE)
    .withBindMounts([
      { source: AUTHORIZATION_DIR, target: "/authz", mode: "ro" },
    ])
    .withWorkingDir("/authz")
    .withCommand([
      "serve",
      "--grpc-preshared-key",
      SPICEDB_PRESHARED_KEY,
      "--datastore-engine",
      "memory",
      "--datastore-bootstrap-files",
      "bootstrap.yaml",
    ])
    .withExposedPorts(50051)
    .withWaitStrategy(Wait.forLogMessage(/grpc server started serving/))
    .start();
}

/** Valkey of the catalog read cache (ADR 40) without persistence, as in kind. */
async function startValkey(): Promise<StartedTestContainer> {
  return new GenericContainer(VALKEY_IMAGE)
    .withCommand(["valkey-server", "--save", "", "--appendonly", "no"])
    .withExposedPorts(6379)
    .withWaitStrategy(Wait.forLogMessage(/Ready to accept connections/))
    .start();
}

/**
 * Global setup of `npm test`: one MongoDB replica set (transactions), one
 * KRaft Kafka broker, one SpiceDB and one Valkey for the whole run. Test files inherit
 * their addresses through the environment and read them with the accessors
 * below; each test world isolates itself by database, topic and ids.
 */
export async function globalSetup(): Promise<void> {
  const kafkaPort = await freePort();
  const [mongo, kafka, spicedb, valkey] = await Promise.all([
    startMongo(),
    startKafka(kafkaPort),
    startSpiceDb(),
    startValkey(),
  ]);
  started.push(mongo, kafka, spicedb, valkey);
  process.env.MONGODB_URI = `${mongo.getConnectionString()}/?directConnection=true`;
  process.env.KAFKA_BROKERS = `localhost:${kafkaPort}`;
  process.env.SPICEDB_ENDPOINT = `${spicedb.getHost()}:${spicedb.getMappedPort(50051)}`;
  process.env.VALKEY_URL = `redis://${valkey.getHost()}:${valkey.getMappedPort(6379)}`;
}

export async function globalTeardown(): Promise<void> {
  await Promise.all(started.map((container) => container.stop()));
}

function required(name: string): string {
  const value = process.env[name]?.trim() ?? "";
  if (value === "") {
    throw new Error(
      `${name} is not set: run tests with --test-global-setup=test/support/global-containers.ts (npm test)`,
    );
  }
  return value;
}

/** Brokers of the Kafka testcontainer. */
export const kafkaBrokers = (): string[] =>
  required("KAFKA_BROKERS").split(",");

/** gRPC endpoint of the SpiceDB testcontainer. */
export const spiceDbEndpoint = (): string => required("SPICEDB_ENDPOINT");

/** `redis://` URL of the Valkey testcontainer. */
export const valkeyUrl = (): string => required("VALKEY_URL");

import { type Meter, metrics } from "@opentelemetry/api";
import type {
  ConnectionCheckedInEvent,
  ConnectionCheckedOutEvent,
  ConnectionCheckOutFailedEvent,
  ConnectionCheckOutStartedEvent,
  ConnectionClosedEvent,
  ConnectionCreatedEvent,
  ConnectionPoolClearedEvent,
  ConnectionPoolClosedEvent,
  ConnectionPoolCreatedEvent,
  ConnectionReadyEvent,
  MongoClient,
} from "mongodb";

// Semconv db.client.connection.* (OTel semantic conventions v1.44.0, Database
// client metrics): the same names and attributes as the PostgreSQL pools of
// orders and payments, so pool panels and alerts work for every store.
const POOL_NAME = "db.client.connection.pool.name";
const STATE = "db.client.connection.state";

// Semconv advice for db.client.connection.{create,wait,use}_time.
const POOL_TIME_BUCKETS = [0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10];

type ConnectionId = number | "<monitor>";

type PoolState = {
  readonly name: string;
  max: number;
  idleMin: number;
  /** Connections created and not yet closed: idle + used. */
  total: number;
  pending: number;
  /** Checked-out connections and their checkout time (performance.now()). */
  readonly checkedOut: Map<ConnectionId, number>;
};

export type DatabasePoolMetricsOptions = Readonly<{
  meter?: Meter;
  /** `db.client.connection.pool.name` of the pool of one server. */
  poolName?: (address: string) => string;
  now?: () => number;
}>;

/**
 * Connection pool metrics of a MongoClient from driver CMAP events
 * (docs/observability/metric-inventory.md#db). The driver keeps one pool per
 * server of the replica set, so every pool is a separate
 * `db.client.connection.pool.name`: saturation is per pool, not a sum over
 * the replica set. Monitoring connections are outside the pools.
 */
export class DatabasePoolMetrics {
  readonly #pools = new Map<string, PoolState>();
  readonly #poolName: (address: string) => string;
  readonly #now: () => number;
  readonly #timeouts;
  readonly #clears;
  readonly #waitTime;
  readonly #createTime;
  readonly #useTime;

  constructor(options: DatabasePoolMetricsOptions = {}) {
    const meter = options.meter ?? metrics.getMeter("mongodb");
    this.#poolName = options.poolName ?? ((address) => address);
    this.#now = options.now ?? (() => performance.now());
    const count = meter.createObservableUpDownCounter(
      "db.client.connection.count",
      {
        description:
          "Connections of the pool by state (idle | used), as of the last CMAP event.",
        unit: "{connection}",
      },
    );
    const max = meter.createObservableUpDownCounter(
      "db.client.connection.max",
      {
        description: "Maximum connections of the pool (maxPoolSize).",
        unit: "{connection}",
      },
    );
    const idleMin = meter.createObservableUpDownCounter(
      "db.client.connection.idle.min",
      {
        description: "Minimum connections kept in the pool (minPoolSize).",
        unit: "{connection}",
      },
    );
    const pending = meter.createObservableUpDownCounter(
      "db.client.connection.pending_requests",
      {
        description: "Checkouts waiting for a connection of the pool.",
        unit: "{request}",
      },
    );
    this.#timeouts = meter.createCounter("db.client.connection.timeouts", {
      description:
        "Checkouts failed after waitQueueTimeoutMS without a free connection.",
      unit: "{timeout}",
    });
    this.#clears = meter.createCounter(
      "nomnom.db.client.connection.pool.clears",
      {
        description:
          "Pool clears: the driver marked the server unhealthy and dropped its connections.",
        unit: "{clear}",
      },
    );
    this.#waitTime = meter.createHistogram("db.client.connection.wait_time", {
      description:
        "Time to obtain a connection from the pool, failed included.",
      unit: "s",
      advice: { explicitBucketBoundaries: POOL_TIME_BUCKETS },
    });
    this.#createTime = meter.createHistogram(
      "db.client.connection.create_time",
      {
        description: "Time to establish a new connection, handshake and auth.",
        unit: "s",
        advice: { explicitBucketBoundaries: POOL_TIME_BUCKETS },
      },
    );
    this.#useTime = meter.createHistogram("db.client.connection.use_time", {
      description: "Time between checkout and check-in of a connection.",
      unit: "s",
      advice: { explicitBucketBoundaries: POOL_TIME_BUCKETS },
    });
    meter.addBatchObservableCallback(
      (result) => {
        for (const pool of this.#pools.values()) {
          const attributes = { [POOL_NAME]: pool.name };
          const used = pool.checkedOut.size;
          result.observe(count, used, { ...attributes, [STATE]: "used" });
          result.observe(count, Math.max(0, pool.total - used), {
            ...attributes,
            [STATE]: "idle",
          });
          result.observe(max, pool.max, attributes);
          result.observe(idleMin, pool.idleMin, attributes);
          result.observe(pending, pool.pending, attributes);
        }
      },
      [count, max, idleMin, pending],
    );
  }

  /** Subscribes to CMAP events; call before `client.connect()`. */
  instrument(client: MongoClient): void {
    client.on("connectionPoolCreated", (event) => this.poolCreated(event));
    client.on("connectionPoolClosed", (event) => this.poolClosed(event));
    client.on("connectionPoolCleared", (event) => this.poolCleared(event));
    client.on("connectionCreated", (event) => this.connectionCreated(event));
    client.on("connectionReady", (event) => this.connectionReady(event));
    client.on("connectionClosed", (event) => this.connectionClosed(event));
    client.on("connectionCheckOutStarted", (event) =>
      this.checkOutStarted(event),
    );
    client.on("connectionCheckOutFailed", (event) =>
      this.checkOutFailed(event),
    );
    client.on("connectionCheckedOut", (event) => this.checkedOut(event));
    client.on("connectionCheckedIn", (event) => this.checkedIn(event));
  }

  poolCreated(event: ConnectionPoolCreatedEvent): void {
    const pool = this.#pool(event.address);
    pool.max = event.options.maxPoolSize;
    pool.idleMin = event.options.minPoolSize;
  }

  poolClosed(event: ConnectionPoolClosedEvent): void {
    this.#pools.delete(event.address);
  }

  poolCleared(event: ConnectionPoolClearedEvent): void {
    this.#clears.add(1, { [POOL_NAME]: this.#pool(event.address).name });
  }

  connectionCreated(event: ConnectionCreatedEvent): void {
    if (event.connectionId === "<monitor>") return;
    this.#pool(event.address).total += 1;
  }

  connectionReady(event: ConnectionReadyEvent): void {
    if (event.connectionId === "<monitor>") return;
    const pool = this.#pool(event.address);
    this.#createTime.record(event.durationMS / 1000, {
      [POOL_NAME]: pool.name,
    });
  }

  connectionClosed(event: ConnectionClosedEvent): void {
    if (event.connectionId === "<monitor>") return;
    const pool = this.#pools.get(event.address);
    if (pool === undefined) return;
    pool.total = Math.max(0, pool.total - 1);
    pool.checkedOut.delete(event.connectionId);
  }

  checkOutStarted(event: ConnectionCheckOutStartedEvent): void {
    this.#pool(event.address).pending += 1;
  }

  checkOutFailed(event: ConnectionCheckOutFailedEvent): void {
    const pool = this.#pool(event.address);
    pool.pending = Math.max(0, pool.pending - 1);
    const attributes = { [POOL_NAME]: pool.name };
    this.#waitTime.record(event.durationMS / 1000, attributes);
    if (event.reason === "timeout") this.#timeouts.add(1, attributes);
  }

  checkedOut(event: ConnectionCheckedOutEvent): void {
    const pool = this.#pool(event.address);
    pool.pending = Math.max(0, pool.pending - 1);
    pool.checkedOut.set(event.connectionId, this.#now());
    this.#waitTime.record(event.durationMS / 1000, { [POOL_NAME]: pool.name });
  }

  checkedIn(event: ConnectionCheckedInEvent): void {
    const pool = this.#pools.get(event.address);
    const since = pool?.checkedOut.get(event.connectionId);
    if (pool === undefined || since === undefined) return;
    pool.checkedOut.delete(event.connectionId);
    this.#useTime.record(Math.max(0, this.#now() - since) / 1000, {
      [POOL_NAME]: pool.name,
    });
  }

  #pool(address: string): PoolState {
    let pool = this.#pools.get(address);
    if (pool === undefined) {
      pool = {
        name: this.#poolName(address),
        max: 0,
        idleMin: 0,
        total: 0,
        pending: 0,
        checkedOut: new Map(),
      };
      this.#pools.set(address, pool);
    }
    return pool;
  }
}

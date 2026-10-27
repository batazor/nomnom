import type { Meter } from "@opentelemetry/api";
import { type Db, MongoClient, type MongoClientOptions } from "mongodb";

import { database } from "../../config/config.ts";
import { DatabaseCommandTelemetry } from "./command-telemetry.ts";
import { DatabasePoolMetrics } from "./pool-metrics.ts";

export type DatabaseConnection = Readonly<{
  client: MongoClient;
  db: Db;
  close(): Promise<void>;
}>;

/** Config without `pool` (tests) gets the defaults of the API process. */
export type DatabaseConnectConfig = Omit<database.Config, "pool"> & {
  readonly pool?: database.PoolConfig;
};

export type DatabaseConnectOptions = Readonly<{
  /** Meter of the pool metrics; the global MeterProvider by default. */
  meter?: Meter;
}>;

/** MongoClient options of the pool; they override the same URI options. */
export function databasePoolOptions(
  pool: database.PoolConfig,
): Pick<
  MongoClientOptions,
  | "maxPoolSize"
  | "minPoolSize"
  | "maxIdleTimeMS"
  | "waitQueueTimeoutMS"
  | "maxConnecting"
> {
  return {
    maxPoolSize: pool.maxPoolSize,
    minPoolSize: pool.minPoolSize,
    maxIdleTimeMS: pool.maxIdleTimeMs,
    waitQueueTimeoutMS: pool.waitQueueTimeoutMs,
    maxConnecting: pool.maxConnecting,
  };
}

/** `db.client.connection.pool.name`: server address and database (semconv). */
export const databasePoolName = (address: string, database: string) =>
  `${address}/${database}`;

/**
 * Opens the client and pings the primary, so an unreachable or misconfigured
 * MongoDB stops startup instead of failing the first request. Pool limits come
 * from MONGODB__* (pkg/config); CMAP events feed the db.client.connection.*
 * metrics, command monitoring events the client spans and
 * db.client.operation.duration (command-telemetry.ts).
 */
export async function connectDatabase(
  config: DatabaseConnectConfig,
  options: DatabaseConnectOptions = {},
): Promise<DatabaseConnection> {
  const pool = config.pool ?? database.loadPool({}, "api");
  const client = new MongoClient(config.uri, {
    appName: "restaurants",
    // Optional domain fields are omitted instead of being stored as null.
    ignoreUndefined: true,
    serverSelectionTimeoutMS: config.timeoutMs,
    // Command events for spans and operation metrics; their payload is not read.
    monitorCommands: true,
    ...databasePoolOptions(pool),
  });
  new DatabaseCommandTelemetry(
    options.meter === undefined ? {} : { meter: options.meter },
  ).instrument(client);
  new DatabasePoolMetrics({
    ...(options.meter === undefined ? {} : { meter: options.meter }),
    poolName: (address) => databasePoolName(address, config.database),
  }).instrument(client);
  await client.connect();
  const db = client.db(config.database);
  try {
    await db.command({ ping: 1 });
  } catch (error) {
    await client.close();
    throw error;
  }
  return Object.freeze({ client, db, close: () => client.close() });
}

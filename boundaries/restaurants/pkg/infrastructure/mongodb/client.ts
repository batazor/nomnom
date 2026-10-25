import type { Meter } from "@opentelemetry/api";
import { type Db, MongoClient, type MongoClientOptions } from "mongodb";

import {
  loadMongoDbPoolConfig,
  type MongoDbEnabledConfig,
  type MongoDbPoolConfig,
} from "../../config/config.ts";
import { MongoPoolMetrics } from "./pool-metrics.ts";

export type MongoDbConnection = Readonly<{
  client: MongoClient;
  db: Db;
  close(): Promise<void>;
}>;

/** Config without `pool` (tests) gets the defaults of the API process. */
export type MongoDbConnectConfig = Omit<MongoDbEnabledConfig, "pool"> & {
  readonly pool?: MongoDbPoolConfig;
};

export type MongoDbConnectOptions = Readonly<{
  /** Meter of the pool metrics; the global MeterProvider by default. */
  meter?: Meter;
}>;

/** MongoClient options of the pool; they override the same URI options. */
export function mongoPoolOptions(
  pool: MongoDbPoolConfig,
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
export const mongoPoolName = (address: string, database: string) =>
  `${address}/${database}`;

/**
 * Opens the client and pings the primary, so an unreachable or misconfigured
 * MongoDB stops startup instead of failing the first request. Pool limits come
 * from MONGODB__* (pkg/config); CMAP events feed the db.client.connection.*
 * metrics.
 */
export async function connectMongoDb(
  config: MongoDbConnectConfig,
  options: MongoDbConnectOptions = {},
): Promise<MongoDbConnection> {
  const pool = config.pool ?? loadMongoDbPoolConfig({}, "api");
  const client = new MongoClient(config.uri, {
    appName: "restaurants",
    // Optional domain fields are omitted instead of being stored as null.
    ignoreUndefined: true,
    serverSelectionTimeoutMS: config.timeoutMs,
    ...mongoPoolOptions(pool),
  });
  new MongoPoolMetrics({
    ...(options.meter === undefined ? {} : { meter: options.meter }),
    poolName: (address) => mongoPoolName(address, config.database),
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

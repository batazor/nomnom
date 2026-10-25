import { type Db, MongoClient } from "mongodb";

import type { MongoDbEnabledConfig } from "../../config/config.ts";

export type MongoDbConnection = Readonly<{
  client: MongoClient;
  db: Db;
  close(): Promise<void>;
}>;

/**
 * Opens the client and pings the primary, so an unreachable or misconfigured
 * MongoDB stops startup instead of failing the first request.
 */
export async function connectMongoDb(
  config: MongoDbEnabledConfig,
): Promise<MongoDbConnection> {
  const client = new MongoClient(config.uri, {
    appName: "restaurants",
    // Optional domain fields are omitted instead of being stored as null.
    ignoreUndefined: true,
    serverSelectionTimeoutMS: config.timeoutMs,
  });
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

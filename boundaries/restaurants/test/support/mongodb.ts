import { randomUUID } from "node:crypto";
import { after } from "node:test";

import { BSON, type Db } from "mongodb";

import { migrateRestaurantsDatabase } from "../../infrastructure/mongodb/collections.ts";
import type {
  AggregateRef,
  PublishableEvent,
} from "../../pkg/application/outbox.ts";
import {
  connectMongoDb,
  type MongoDbConnection,
} from "../../pkg/infrastructure/mongodb/client.ts";
import {
  MongoOutbox,
  OUTBOX_COLLECTION,
  type OutboxDocument,
  type OutboxEventMapper,
} from "../../pkg/infrastructure/mongodb/outbox.ts";
import { MongoUnitOfWork } from "../../pkg/infrastructure/mongodb/unit-of-work.ts";

/**
 * MONGODB_URI comes from the global setup of `npm test`
 * (test/support/global-mongodb.ts): a replica set in a testcontainer.
 */
export const mongoUri = (): string => {
  const uri = process.env.MONGODB_URI?.trim() ?? "";
  if (uri === "") {
    throw new Error(
      "MONGODB_URI is not set: run tests with --test-global-setup=test/support/global-mongodb.ts (npm test)",
    );
  }
  return uri;
};

let connection: Promise<MongoDbConnection> | undefined;
const databases: Db[] = [];

after(async () => {
  if (connection === undefined) return;
  const mongo = await connection;
  for (const db of databases) await db.dropDatabase();
  await mongo.close();
});

/** One client per test process; databases isolate the test worlds. */
const sharedConnection = (): Promise<MongoDbConnection> => {
  connection ??= connectMongoDb({
    enabled: true,
    uri: mongoUri(),
    database: "restaurants_test",
    timeoutMs: 10_000,
  });
  return connection;
};

export type TestDatabase = Readonly<{
  mongo: MongoDbConnection;
  db: Db;
  unitOfWork: MongoUnitOfWork;
}>;

/**
 * Fresh migrated database with a unique name, dropped after the test file.
 * `mongo.db` is this database, so code taking a connection uses it too.
 */
export const createTestDatabase = async (): Promise<TestDatabase> => {
  const shared = await sharedConnection();
  const db = shared.client.db(`restaurants_test_${randomUUID().slice(0, 12)}`);
  databases.push(db);
  await migrateRestaurantsDatabase(db);
  const mongo: MongoDbConnection = {
    client: shared.client,
    db,
    close: async () => {},
  };
  return { mongo, db, unitOfWork: new MongoUnitOfWork(shared.client) };
};

export type RecordedOutboxRecord = Readonly<{
  aggregate: AggregateRef;
  event: PublishableEvent;
}>;

/** Writes every Domain Event with its aggregate as EJSON payload. */
const recordingMapper: OutboxEventMapper = (aggregate, event) => ({
  eventType: event.type,
  topic: "restaurants.test",
  payload: new TextEncoder().encode(BSON.EJSON.stringify({ aggregate, event })),
});

/**
 * MongoDB Outbox of the test worlds: every Domain Event is written in the
 * transaction of the Unit of Work and read back in `seq` order.
 */
export class RecordedOutbox extends MongoOutbox {
  readonly #db: Db;

  constructor(db: Db, unitOfWork: MongoUnitOfWork) {
    super(db, unitOfWork, { mapper: recordingMapper });
    this.#db = db;
  }

  async records(): Promise<RecordedOutboxRecord[]> {
    const documents = await this.#db
      .collection<OutboxDocument>(OUTBOX_COLLECTION)
      .find({})
      .sort({ seq: 1 })
      .toArray();
    return documents.map(
      (document) =>
        BSON.EJSON.parse(
          new TextDecoder().decode(document.payload.buffer),
        ) as RecordedOutboxRecord,
    );
  }

  async eventTypes(): Promise<string[]> {
    return (await this.records()).map((record) => record.event.type);
  }
}

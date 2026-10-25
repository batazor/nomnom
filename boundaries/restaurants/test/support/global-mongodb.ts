import {
  MongoDBContainer,
  type StartedMongoDBContainer,
} from "@testcontainers/mongodb";

/** Image of the kind bootstrap (ops/kind/bootstrap/values.yaml). */
const MONGODB_IMAGE = "mongo:8.3.13";

let container: StartedMongoDBContainer | undefined;

/**
 * Global setup of `npm test`: one single-node replica set (transactions) for
 * the whole run. Test files inherit MONGODB_URI and isolate themselves with a
 * database per test world (test/support/mongodb.ts).
 */
export async function globalSetup(): Promise<void> {
  container = await new MongoDBContainer(MONGODB_IMAGE).start();
  process.env.MONGODB_URI = `${container.getConnectionString()}/?directConnection=true`;
}

export async function globalTeardown(): Promise<void> {
  await container?.stop();
}

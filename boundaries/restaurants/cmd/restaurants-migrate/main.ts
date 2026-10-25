// Command restaurants-migrate creates the MongoDB collections of restaurants
// with validators and indexes, applies pending document migrations and exits.
// In Kubernetes it runs as the `migrate` init container before the workload;
// every step is idempotent, so concurrent runs are safe.
import { migrateRestaurantsDatabase } from "../../infrastructure/mongodb/collections.ts";
import { loadConfig, loadLocalEnv } from "../../pkg/config/config.ts";
import { connectMongoDb } from "../../pkg/infrastructure/mongodb/client.ts";

loadLocalEnv();
const config = loadConfig();
if (!config.mongodb.enabled) {
  throw new Error(
    "MONGODB__URI is required: restaurants stores its data in MongoDB",
  );
}
const mongo = await connectMongoDb(config.mongodb);
try {
  await migrateRestaurantsDatabase(mongo.db, (message) =>
    console.log(`restaurants-migrate ${message}`),
  );
  console.log("restaurants-migrate collections and indexes are up to date");
} finally {
  await mongo.close();
}

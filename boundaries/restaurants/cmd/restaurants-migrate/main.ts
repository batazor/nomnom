// Command restaurants-migrate creates the MongoDB collections of restaurants
// with validators and indexes, applies pending document migrations and exits.
// In Kubernetes it runs as the `migrate` init container before the workload;
// every step is idempotent, so concurrent runs are safe. The object graph is
// composed by Awilix in di/migrate.ts (ADR 5); this composition root owns the
// MongoDB connection.
import { createMigrateContainer } from "../../di/migrate.ts";
import { loadConfig, loadLocalEnv } from "../../pkg/config/config.ts";
import { connectMongoDb } from "../../pkg/infrastructure/mongodb/client.ts";
import { createLogger, logUncaughtErrors } from "../../pkg/telemetry/logger.ts";
import { telemetryResource } from "../../pkg/telemetry/resource.ts";

loadLocalEnv();
// A one-shot init container: JSON logs only, no OTLP export.
const logger = createLogger({
  resource: telemetryResource("restaurants-migrate"),
}).child({ operation: "migrate" });
logUncaughtErrors(logger);
const config = loadConfig(process.env, { process: "migrate" });
if (!config.mongodb.enabled) {
  throw new Error(
    "MONGODB__URI is required: restaurants stores its data in MongoDB",
  );
}
const mongo = await connectMongoDb(config.mongodb);
const container = createMigrateContainer({
  db: mongo.db,
  logger,
});
try {
  await container.cradle.migration.run();
  logger.info("collections and indexes are up to date");
} finally {
  await container.dispose();
  await mongo.close();
}

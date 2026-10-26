// Command restaurants serves the Connect/gRPC API of restaurants with the
// relationship reconciler in the background. The object graph is composed by
// Awilix in di/restaurants.ts (ADR 5); this composition root owns the
// asynchronously acquired resources and the shutdown order.
import { createRestaurantsContainer } from "../../di/restaurants.ts";
import { createSubjectResolver } from "../../infrastructure/grpc/subject.ts";
import { migrateRestaurantsDatabase } from "../../infrastructure/repository/migrate.ts";
import {
  loadConfig,
  loadLocalEnv,
  loadSimulatorConfig,
} from "../../pkg/config/config.ts";
import { connectDatabase } from "../../pkg/infrastructure/repository/client.ts";
import { connectValkey } from "../../pkg/infrastructure/valkey/client.ts";
import { Lifecycle } from "../../pkg/lifecycle/lifecycle.ts";
import { createLogger, logUncaughtErrors } from "../../pkg/telemetry/logger.ts";
import { telemetryResource } from "../../pkg/telemetry/resource.ts";
import { startTelemetry } from "../../pkg/telemetry/telemetry.ts";

const SERVICE_NAME = "restaurants";

loadLocalEnv();
// JSON logs to stdout with the telemetry resource; a startup failure is one
// ERROR record.
const logger = createLogger({ resource: telemetryResource(SERVICE_NAME) });
logUncaughtErrors(logger);
const config = loadConfig();
// Demo Simulator (FR-606): required in this process; an empty or invalid
// value or MIN_DELAY > MAX_DELAY stops startup.
const simulator = loadSimulatorConfig(process.env);
// MongoDB (ADR 8, local ADR 1) is required; the process migrates its
// collections, validators and indexes itself before serving (local ADR 2).
if (!config.database.enabled) {
  throw new Error(
    "MONGODB__URI is required: restaurants stores its data in MongoDB",
  );
}
// SpiceDB (ADR 18) is required; errors and timeouts fail closed as
// UNAVAILABLE. Without SPICEDB__ENDPOINT the process does not start.
if (!config.spicedb.enabled) {
  throw new Error(
    "SPICEDB__ENDPOINT is required: restaurants checks permissions in SpiceDB",
  );
}
// Valkey (ADR 40, local ADR 3) caches Get Restaurant and Get Menu. The URL is
// required; an unreachable Valkey only makes reads bypass the cache.
if (!config.valkey.enabled) {
  throw new Error(
    "VALKEY__URL is required: restaurants caches catalog reads in Valkey",
  );
}
// Telemetry starts before the application (docs/cloud-native-runtime.md):
// OTLP metrics and traces are exported only with OTEL_EXPORTER_OTLP_*.
const telemetry = startTelemetry(SERVICE_NAME);
// Projected ServiceAccount token verification (ADR 27) and BFF user
// assertions (ADR 32). Missing issuer, audience or JWKS, an invalid JWKS or
// allowlist stops startup.
const identityLog = logger.child({ operation: "identity" });
const subjects = await createSubjectResolver(
  config.identity,
  identityLog.warn,
  { audit: identityLog.info },
);
const database = await connectDatabase(config.database);
const valkey = connectValkey(config.valkey, {
  connectionName: SERVICE_NAME,
  logger: logger.child({ operation: "valkey" }),
});
const container = createRestaurantsContainer({
  server: config.server,
  spicedb: config.spicedb,
  reconciler: config.reconciler,
  kitchen: config.kitchen,
  simulator,
  database,
  valkeyConfig: config.valkey,
  valkey: valkey.client,
  subjects,
  logger,
});

try {
  // Nothing to change on an already current database; a failed migration
  // stops the process before it serves.
  await migrateRestaurantsDatabase(
    database.db,
    logger.child({ operation: "migrate" }).info,
  );
  await new Lifecycle({
    managementPort: config.management.port,
    readinessDelayMs: config.shutdown.readinessDelayMs,
    drainTimeoutMs: config.shutdown.drainTimeoutMs,
  }).run(container.cradle.worker, {
    log: logger.child({ operation: "lifecycle" }).info,
  });
} catch (error) {
  logger.error("stopped with error", { operation: "lifecycle", error });
  process.exitCode = 1;
} finally {
  // The container closes the SpiceDB client before MongoDB disconnects;
  // telemetry is flushed last, within its own deadline.
  await container.dispose();
  await valkey.close();
  await database.close();
  await telemetry.shutdown(config.shutdown.telemetryFlushTimeoutMs);
}

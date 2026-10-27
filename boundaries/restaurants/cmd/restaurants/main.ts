// Command restaurants serves the Connect/gRPC API of restaurants with the
// relationship reconciler in the background. The object graph is composed by
// Awilix in di/restaurants.ts (ADR 5); this composition root owns the
// asynchronously acquired resources and the shutdown order.
import { createRestaurantsContainer } from "../../di/restaurants.ts";
import { createSubjectResolver } from "../../infrastructure/grpc/subject.ts";
import { migrateRestaurantsDatabase } from "../../infrastructure/repository/migrate.ts";
import {
  database as databaseConfig,
  loadConfig,
  loadIdentityConfig,
  loadLocalEnv,
  loadSimulatorConfig,
  loadSpiceDbConfig,
  loadValkeyConfig,
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
// Required infrastructure: an empty or invalid value stops startup.
// MongoDB (ADR 8, local ADR 1): the process migrates its collections,
// validators and indexes itself before serving (local ADR 2).
const mongodb = databaseConfig.load(process.env, "api");
// SpiceDB (ADR 18): errors and timeouts fail closed as UNAVAILABLE.
const spicedb = loadSpiceDbConfig(process.env);
// Valkey (ADR 43, local ADR 3) caches Get Restaurant and Get Menu; an
// unreachable Valkey only makes reads bypass the cache.
const valkeyConfig = loadValkeyConfig(process.env);
// Projected ServiceAccount tokens (ADR 27) and BFF user assertions (ADR 32).
const identity = loadIdentityConfig(process.env);
// Telemetry starts before the application (docs/cloud-native-runtime.md):
// OTLP metrics and traces are exported only with OTEL_EXPORTER_OTLP_*.
const telemetry = startTelemetry(SERVICE_NAME);
// Projected ServiceAccount token verification (ADR 27) and BFF user
// assertions (ADR 32). Missing issuer, audience or JWKS, an invalid JWKS or
// allowlist stops startup.
const identityLog = logger.child({ operation: "identity" });
const subjects = await createSubjectResolver(identity, identityLog.warn, {
  audit: identityLog.info,
});
const database = await connectDatabase(mongodb);
const valkey = connectValkey(valkeyConfig, {
  connectionName: SERVICE_NAME,
  logger: logger.child({ operation: "valkey" }),
});
const container = createRestaurantsContainer({
  server: config.server,
  spicedb,
  reconciler: config.reconciler,
  kitchen: config.kitchen,
  waitTime: config.waitTime,
  simulator,
  database,
  valkeyConfig,
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

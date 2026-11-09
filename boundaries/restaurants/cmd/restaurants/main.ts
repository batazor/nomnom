// Command restaurants serves the Connect/gRPC API of restaurants with the
// relationship reconciler in the background. The object graph is composed by
// Awilix in di/restaurants.ts (ADR 5); this composition root owns the
// asynchronously acquired resources and the shutdown order.
import { createRestaurantsContainer } from "../../di/restaurants.ts";
import { createSubjectResolver } from "../../infrastructure/grpc/subject.ts";
import { connectDatabase } from "../../infrastructure/repository/client.ts";
import { migrateRestaurantsDatabase } from "../../infrastructure/repository/migrate.ts";
import { connectValkey } from "../../infrastructure/valkey/client.ts";
import {
  database as databaseConfig,
  loadConfig,
  loadIdentityConfig,
  loadLocalEnv,
  loadScheduledSlotConfig,
  loadSimulatorConfig,
  loadSpiceDbConfig,
  loadTelemetryConfig,
  loadValkeyConfig,
  readConfigFile,
} from "../../pkg/config/config.ts";
import { Lifecycle } from "../../pkg/lifecycle/lifecycle.ts";
import {
  createLogger,
  logStartupFailure,
  logUncaughtErrors,
} from "../../pkg/telemetry/logger.ts";
import { telemetryResource } from "../../pkg/telemetry/resource.ts";
import { startTelemetry } from "../../pkg/telemetry/telemetry.ts";

const SERVICE_NAME = "restaurants";

// Secrets of a local run from .env; everything else comes from CONFIG_FILE.
loadLocalEnv();
// The configuration file (ADR 17) is read before anything else: a missing
// file, another role, an unknown key or an invalid value is one ERROR record
// and exit 1 before a port is opened.
const loaded = logStartupFailure(SERVICE_NAME, () => {
  const spec = readConfigFile(process.env, SERVICE_NAME);
  return {
    config: loadConfig(spec),
    telemetry: loadTelemetryConfig(spec, process.env, SERVICE_NAME),
    // Demo Simulator (FR-606): required in this process; a missing or
    // invalid value or minDelay > maxDelay stops startup.
    simulator: loadSimulatorConfig(spec),
    // Delivery Windows of scheduled orders (FR-694): required; a missing or
    // invalid value, a width not dividing 30 minutes or minLead >= maxHorizon
    // stops startup.
    scheduledSlot: loadScheduledSlotConfig(spec),
    // Required infrastructure: a missing or invalid value stops startup.
    // MongoDB (ADR 8, local ADR 1): the process migrates its collections,
    // validators and indexes itself before serving (local ADR 2).
    mongodb: databaseConfig.load(spec, process.env, "api"),
    // SpiceDB (ADR 18): errors and timeouts fail closed as UNAVAILABLE.
    spicedb: loadSpiceDbConfig(spec, process.env),
    // Valkey (ADR 43, local ADR 3) caches Get Restaurant and Get Menu; an
    // unreachable Valkey only makes reads bypass the cache.
    valkeyConfig: loadValkeyConfig(spec, process.env),
    // Projected ServiceAccount tokens (ADR 27) and BFF user assertions (ADR 32).
    identity: loadIdentityConfig(spec),
  };
});
const {
  config,
  simulator,
  scheduledSlot,
  mongodb,
  spicedb,
  valkeyConfig,
  identity,
} = loaded;
// JSON logs to stdout with the telemetry resource and spec.logging.level; an
// uncaught error is one ERROR record.
const logger = createLogger({
  resource: telemetryResource(loaded.telemetry),
  level: loaded.telemetry.logLevel,
});
logUncaughtErrors(logger);
// Telemetry starts before the application (docs/cloud-native-runtime.md):
// OTLP metrics and traces are exported only with spec.metrics.enabled and
// spec.tracing.enabled.
const telemetry = startTelemetry(loaded.telemetry);
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
  scheduledSlot,
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

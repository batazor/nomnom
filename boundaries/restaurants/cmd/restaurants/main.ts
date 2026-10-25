// Command restaurants serves the Connect/gRPC API of restaurants with the
// relationship reconciler in the background. The object graph is composed by
// Awilix in di/restaurants.ts (ADR 5); this composition root owns the
// asynchronously acquired resources and the shutdown order.
import { createRestaurantsContainer } from "../../di/restaurants.ts";
import { createSubjectResolver } from "../../infrastructure/grpc/subject.ts";
import { loadConfig, loadLocalEnv } from "../../pkg/config/config.ts";
import { connectMongoDb } from "../../pkg/infrastructure/mongodb/client.ts";
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
// MongoDB (ADR 8, local ADR 1) is required; collections, validators and
// indexes are created by restaurants-migrate before the process starts.
if (!config.mongodb.enabled) {
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
// Telemetry starts before the application (docs/cloud-native-runtime.md):
// OTLP metrics and traces are exported only with OTEL_EXPORTER_OTLP_*.
const telemetry = startTelemetry(SERVICE_NAME);
// Projected ServiceAccount token verification (ADR 27) and BFF user
// assertions (ADR 32). Without issuer, audience and JWKS the
// RejectingSubjectResolver keeps commands fail closed; an invalid JWKS or
// allowlist stops startup.
const identityLog = logger.child({ operation: "identity" });
const subjects = await createSubjectResolver(
  config.identity,
  identityLog.warn,
  { audit: identityLog.info },
);
const mongo = await connectMongoDb(config.mongodb);
const container = createRestaurantsContainer({
  server: config.server,
  spicedb: config.spicedb,
  reconciler: config.reconciler,
  kitchen: config.kitchen,
  mongo,
  subjects,
  logger,
});

try {
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
  await mongo.close();
  await telemetry.shutdown(config.shutdown.telemetryFlushTimeoutMs);
}

// Command restaurants serves the Connect/gRPC API of restaurants with the
// relationship reconciler in the background. The object graph is composed by
// Awilix in di/restaurants.ts (ADR 5); this composition root owns the
// asynchronously acquired resources and the shutdown order.
import { createRestaurantsContainer } from "../../di/restaurants.ts";
import { createSubjectResolver } from "../../infrastructure/grpc/subject.ts";
import { loadConfig, loadLocalEnv } from "../../pkg/config/config.ts";
import { connectMongoDb } from "../../pkg/infrastructure/mongodb/client.ts";
import { Lifecycle } from "../../pkg/lifecycle/lifecycle.ts";
import { startTelemetry } from "../../pkg/telemetry/telemetry.ts";

loadLocalEnv();
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
// Projected ServiceAccount token verification (ADR 27) and BFF user
// assertions (ADR 32). Without issuer, audience and JWKS the
// RejectingSubjectResolver keeps commands fail closed; an invalid JWKS or
// allowlist stops startup.
const subjects = await createSubjectResolver(
  config.identity,
  (message) => console.warn(`restaurants ${message}`),
  { audit: (message) => console.info(`restaurants ${message}`) },
);
// OTLP metrics (business counters of Kitchen Orders among them) are exported
// only when OTEL_EXPORTER_OTLP_ENDPOINT is set.
const telemetry = startTelemetry("restaurants");
const mongo = await connectMongoDb(config.mongodb);
const container = createRestaurantsContainer({
  server: config.server,
  spicedb: config.spicedb,
  reconciler: config.reconciler,
  kitchen: config.kitchen,
  mongo,
  subjects,
});

try {
  await new Lifecycle({
    managementPort: config.management.port,
    readinessDelayMs: config.shutdown.readinessDelayMs,
    drainTimeoutMs: config.shutdown.drainTimeoutMs,
  }).run(container.cradle.worker, {
    log: (message) => console.log(`restaurants ${message}`),
  });
} catch (error) {
  console.error("restaurants stopped with error", error);
  process.exitCode = 1;
} finally {
  // The container closes the SpiceDB client before MongoDB disconnects.
  await container.dispose();
  await mongo.close();
  await telemetry.shutdown();
}

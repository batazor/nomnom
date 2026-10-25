import { loadEnvFile } from "node:process";
import {
  type KafkaConfig,
  loadKafkaConfig,
  loadMongoDbConfig,
  loadSpiceDbConfig,
  type MongoDbConfig,
  type SpiceDbConfig,
} from "./connections.ts";
import {
  DEFAULT_DRAIN_TIMEOUT,
  DEFAULT_MANAGEMENT_PORT,
  DEFAULT_PORT,
  DEFAULT_READINESS_DELAY,
  DEFAULT_TELEMETRY_FLUSH_TIMEOUT,
  DRAIN_TIMEOUT_ENV,
  MANAGEMENT_PORT_ENV,
  type MongoDbProcess,
  PORT_ENV,
  READINESS_DELAY_ENV,
  TELEMETRY_FLUSH_TIMEOUT_ENV,
} from "./env.ts";
import { type IdentityConfig, loadIdentityConfig } from "./identity.ts";
import { type KitchenConfig, loadKitchenConfig } from "./kitchen.ts";
import { durationSchema, portSchema } from "./schemas.ts";
import {
  type ConsumerConfig,
  loadConsumerConfig,
  loadOutboxRelayConfig,
  loadReconcilerConfig,
  type OutboxRelayConfig,
  type ReconcilerConfig,
} from "./workers.ts";

export * from "./connections.ts";
export * from "./env.ts";
export * from "./identity.ts";
export * from "./kitchen.ts";
export * from "./workers.ts";

export interface ServerConfig {
  readonly port: number;
}

/** Технический порт (Kubernetes-имя `metrics`) с health и metrics. */
export interface ManagementConfig {
  readonly port: number;
}

export interface ShutdownConfig {
  readonly readinessDelayMs: number;
  readonly drainTimeoutMs: number;
  /** Deadline of the final telemetry export after the drain. */
  readonly telemetryFlushTimeoutMs: number;
}

export interface AppConfig {
  readonly server: ServerConfig;
  readonly management: ManagementConfig;
  readonly shutdown: ShutdownConfig;
  readonly identity: IdentityConfig;
  readonly spicedb: SpiceDbConfig;
  readonly mongodb: MongoDbConfig;
  readonly reconciler: ReconcilerConfig;
  readonly kitchen: KitchenConfig;
  readonly kafka: KafkaConfig;
  readonly outbox: OutboxRelayConfig;
  readonly consumer: ConsumerConfig;
}

export function loadLocalEnv(path = ".env"): void {
  try {
    loadEnvFile(path);
  } catch (error) {
    if (
      !(error instanceof Error && "code" in error && error.code === "ENOENT")
    ) {
      throw error;
    }
  }
}

export type LoadConfigOptions = Readonly<{
  /** Процесс, по которому выбирается default `MONGODB__MAX_POOL_SIZE`. */
  process?: MongoDbProcess;
}>;

export function loadConfig(
  environment: Record<string, string | undefined> = process.env,
  options: LoadConfigOptions = {},
): AppConfig {
  const server = loadServerConfig(environment);
  const management = Object.freeze({
    port: portSchema.parse(
      environment[MANAGEMENT_PORT_ENV] ?? DEFAULT_MANAGEMENT_PORT,
    ),
  });
  if (management.port === server.port) {
    throw new Error(
      `${MANAGEMENT_PORT_ENV} must differ from server port ${server.port}`,
    );
  }
  return Object.freeze({
    server,
    management,
    shutdown: loadShutdownConfig(environment),
    identity: loadIdentityConfig(environment),
    spicedb: loadSpiceDbConfig(environment),
    mongodb: loadMongoDbConfig(environment, options.process),
    reconciler: loadReconcilerConfig(environment),
    kitchen: loadKitchenConfig(environment),
    kafka: loadKafkaConfig(environment),
    outbox: loadOutboxRelayConfig(environment),
    consumer: loadConsumerConfig(environment),
  });
}

function loadShutdownConfig(
  environment: Record<string, string | undefined>,
): ShutdownConfig {
  const readinessDelayMs = durationSchema.parse(
    environment[READINESS_DELAY_ENV] ?? DEFAULT_READINESS_DELAY,
  );
  const drainTimeoutMs = durationSchema
    .refine((value) => value > 0, "drain timeout must be positive")
    .parse(environment[DRAIN_TIMEOUT_ENV] ?? DEFAULT_DRAIN_TIMEOUT);
  const telemetryFlushTimeoutMs = durationSchema
    .refine((value) => value > 0, "telemetry flush timeout must be positive")
    .parse(
      environment[TELEMETRY_FLUSH_TIMEOUT_ENV] ??
        DEFAULT_TELEMETRY_FLUSH_TIMEOUT,
    );

  return Object.freeze({
    readinessDelayMs,
    drainTimeoutMs,
    telemetryFlushTimeoutMs,
  });
}

function loadServerConfig(
  environment: Record<string, string | undefined>,
): ServerConfig {
  const port = portSchema.parse(
    environment[PORT_ENV] ?? environment.PORT ?? DEFAULT_PORT,
  );

  return Object.freeze({ port });
}

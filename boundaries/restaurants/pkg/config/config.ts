import { loadEnvFile } from "node:process";
import {
  DEFAULT_DRAIN_TIMEOUT,
  DEFAULT_MANAGEMENT_PORT,
  DEFAULT_PORT,
  DEFAULT_READINESS_DELAY,
  DEFAULT_TELEMETRY_FLUSH_TIMEOUT,
  DRAIN_TIMEOUT_ENV,
  MANAGEMENT_PORT_ENV,
  PORT_ENV,
  READINESS_DELAY_ENV,
  TELEMETRY_FLUSH_TIMEOUT_ENV,
} from "./env.ts";
import { type KitchenConfig, loadKitchenConfig } from "./kitchen.ts";
import { durationSchema, portSchema } from "./schemas.ts";
import { loadWaitTimeConfig, type WaitTimeConfig } from "./wait-time.ts";
import {
  type ConsumerConfig,
  loadConsumerConfig,
  loadReconcilerConfig,
  type ReconcilerConfig,
} from "./workers.ts";

export * from "./cache.ts";
export * from "./connections.ts";
export * as database from "./database.ts";
export * from "./env.ts";
export * from "./identity.ts";
export * from "./kitchen.ts";
export * from "./simulator.ts";
export * from "./wait-time.ts";
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

/**
 * Tunables of the processes with explicit defaults. Required infrastructure
 * (MongoDB, Valkey, SpiceDB, Kafka, identity) is loaded by each process that
 * needs it with its own loader, which stops startup on an empty value.
 */
export interface AppConfig {
  readonly server: ServerConfig;
  readonly management: ManagementConfig;
  readonly shutdown: ShutdownConfig;
  readonly reconciler: ReconcilerConfig;
  readonly kitchen: KitchenConfig;
  readonly waitTime: WaitTimeConfig;
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

export function loadConfig(
  environment: Record<string, string | undefined> = process.env,
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
    reconciler: loadReconcilerConfig(environment),
    kitchen: loadKitchenConfig(environment),
    waitTime: loadWaitTimeConfig(environment),
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

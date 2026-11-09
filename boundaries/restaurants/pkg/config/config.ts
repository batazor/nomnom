import { loadEnvFile } from "node:process";

import type { Spec } from "./file.ts";
import { type KitchenConfig, loadKitchenConfig } from "./kitchen.ts";
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
export * from "./defaults.ts";
export * from "./file.ts";
export * from "./identity.ts";
export * from "./kitchen.ts";
export * from "./scheduled-slot.ts";
export * from "./simulator.ts";
export * from "./telemetry.ts";
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
 * needs it with its own loader, which stops startup on a missing value.
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

/**
 * Secrets of a local run from `.env` (MONGODB__URI, VALKEY__URL,
 * SPICEDB__PRESHARED_KEY); variables already set win. Non-secret settings
 * come only from the configuration file.
 */
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

export function loadConfig(spec: Spec): AppConfig {
  if (spec.management.port === spec.server.port) {
    throw new Error(
      `spec.management.port must differ from spec.server.port ${spec.server.port}`,
    );
  }
  return Object.freeze({
    server: Object.freeze({ port: spec.server.port }),
    management: Object.freeze({ port: spec.management.port }),
    shutdown: Object.freeze({
      readinessDelayMs: spec.shutdown.readinessDelay,
      drainTimeoutMs: spec.shutdown.drainTimeout,
      telemetryFlushTimeoutMs: spec.shutdown.telemetryFlushTimeout,
    }),
    reconciler: loadReconcilerConfig(spec),
    kitchen: loadKitchenConfig(spec),
    waitTime: loadWaitTimeConfig(spec),
    consumer: loadConsumerConfig(spec),
  });
}

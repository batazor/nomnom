// Versioned configuration file of a restaurants process (ADR 17): a YAML
// document shaped like a Kubernetes resource, `apiVersion`, `kind`,
// `metadata.name` (the process role) and `spec`. Every non-secret setting
// comes from it; secrets come only from env (Kubernetes Secret) and have no
// field in `spec`, so a secret in the file is an unknown key. Every object is
// strict: an unknown key at any level stops startup with its path.
import { readFileSync } from "node:fs";

import { parse } from "yaml";
import { type core, z } from "zod";

import { cacheSectionSchema, valkeySectionSchema } from "./cache.ts";
import {
  kafkaSectionSchema,
  schemaRegistrySectionSchema,
  spicedbSectionSchema,
} from "./connections.ts";
import { mongodbSectionSchema } from "./database.ts";
import {
  DEFAULT_DRAIN_TIMEOUT,
  DEFAULT_MANAGEMENT_PORT,
  DEFAULT_PORT,
  DEFAULT_READINESS_DELAY,
  DEFAULT_TELEMETRY_FLUSH_TIMEOUT,
} from "./defaults.ts";
import { identitySectionSchema } from "./identity.ts";
import { kitchenQueueSectionSchema, kitchenSectionSchema } from "./kitchen.ts";
import { scheduledSlotSectionSchema } from "./scheduled-slot.ts";
import {
  durationSchema,
  portSchema,
  positiveDurationSchema,
} from "./schemas.ts";
import { simulatorSectionSchema } from "./simulator.ts";
import {
  loggingSectionSchema,
  metricsSectionSchema,
  telemetrySectionSchema,
  tracingSectionSchema,
} from "./telemetry.ts";
import { waitTimeSectionSchema } from "./wait-time.ts";
import { consumerSectionSchema, reconcilerSectionSchema } from "./workers.ts";

/** Path of the configuration file. */
export const CONFIG_FILE_ENV = "CONFIG_FILE";
export const CONFIG_API_VERSION = "config.nomnom.dev/v1alpha1";
export const CONFIG_KIND = "Restaurants";

/** Roles of the processes; `metadata.name` of the file. */
export type Role = "restaurants" | "restaurants-consumer";

/**
 * Who reads the file: a process checks `metadata.name` against its role;
 * restaurants-admin runs in the pod of another role and checks only
 * `apiVersion` and `kind`.
 */
export type Reader = Role | "restaurants-admin";

/**
 * `spec` of the kind Restaurants: the sections of every role. A process reads
 * only its own sections, but the whole `spec` is checked for unknown keys.
 * Keys follow the former env names in camelCase (`MONGODB__MAX_POOL_SIZE` →
 * `mongodb.maxPoolSize`); durations are strings like `5s`.
 */
export const specSchema = z.strictObject({
  server: z
    .strictObject({ port: portSchema.prefault(DEFAULT_PORT) })
    .prefault({}),
  /** Технический порт (Kubernetes-имя `metrics`) с health и metrics. */
  management: z
    .strictObject({ port: portSchema.prefault(DEFAULT_MANAGEMENT_PORT) })
    .prefault({}),
  shutdown: z
    .strictObject({
      readinessDelay: durationSchema.prefault(DEFAULT_READINESS_DELAY),
      drainTimeout: positiveDurationSchema.prefault(DEFAULT_DRAIN_TIMEOUT),
      /** Deadline of the final telemetry export after the drain. */
      telemetryFlushTimeout: positiveDurationSchema.prefault(
        DEFAULT_TELEMETRY_FLUSH_TIMEOUT,
      ),
    })
    .prefault({}),
  telemetry: telemetrySectionSchema,
  tracing: tracingSectionSchema,
  metrics: metricsSectionSchema,
  logging: loggingSectionSchema,
  identity: identitySectionSchema,
  spicedb: spicedbSectionSchema,
  mongodb: mongodbSectionSchema,
  valkey: valkeySectionSchema,
  cache: cacheSectionSchema,
  reconciler: reconcilerSectionSchema,
  kitchen: kitchenSectionSchema,
  kitchenQueue: kitchenQueueSectionSchema,
  waitTime: waitTimeSectionSchema,
  simulator: simulatorSectionSchema,
  scheduledSlot: scheduledSlotSectionSchema,
  kafka: kafkaSectionSchema,
  schemaRegistry: schemaRegistrySectionSchema,
  consumer: consumerSectionSchema,
});

export type Spec = z.output<typeof specSchema>;

const documentSchema = z.strictObject({
  apiVersion: z.literal(CONFIG_API_VERSION),
  kind: z.literal(CONFIG_KIND),
  metadata: z.strictObject({ name: z.string() }),
  spec: specSchema,
});

/**
 * Reads the file at `CONFIG_FILE` and returns its validated `spec`. A missing
 * or unreadable file, another `apiVersion`, `kind` or role and an unknown or
 * invalid key throw before the process opens a port.
 */
export function readConfigFile(
  environment: Readonly<Record<string, string | undefined>>,
  reader: Reader,
): Spec {
  const path = environment[CONFIG_FILE_ENV]?.trim() ?? "";
  if (path === "") {
    throw new Error(
      `${CONFIG_FILE_ENV} is required: the path of the process configuration file`,
    );
  }
  let text: string;
  try {
    text = readFileSync(path, "utf8");
  } catch (error) {
    throw new Error(`${CONFIG_FILE_ENV} ${path}: cannot read the file`, {
      cause: error,
    });
  }
  let document: unknown;
  try {
    document = parse(text);
  } catch (error) {
    throw new Error(`${path}: invalid YAML`, { cause: error });
  }
  try {
    return parseConfigDocument(document, reader);
  } catch (error) {
    throw new Error(`${path}: ${(error as Error).message}`);
  }
}

/** Validates a parsed configuration document for `reader`. */
export function parseConfigDocument(document: unknown, reader: Reader): Spec {
  const header = z
    .looseObject({ apiVersion: z.unknown(), kind: z.unknown() })
    .safeParse(document);
  if (
    !header.success ||
    header.data.apiVersion !== CONFIG_API_VERSION ||
    header.data.kind !== CONFIG_KIND
  ) {
    const got = header.success
      ? `apiVersion ${String(header.data.apiVersion)} and kind ${String(header.data.kind)}`
      : "no mapping";
    throw new Error(
      `expected apiVersion ${CONFIG_API_VERSION} and kind ${CONFIG_KIND}, got ${got}`,
    );
  }
  const parsed = documentSchema.safeParse(document);
  if (!parsed.success) {
    throw new Error(parsed.error.issues.flatMap(describeIssue).join("; "));
  }
  const { name } = parsed.data.metadata;
  if (reader !== "restaurants-admin" && name !== reader) {
    throw new Error(
      `metadata.name: expected the role ${reader}, got "${name}"`,
    );
  }
  return parsed.data.spec;
}

/** `spec.shutdown.drainTimout: unknown field`, one line per key. */
function describeIssue(issue: core.$ZodIssue): string[] {
  const path = issue.path.map(String).join(".");
  if (issue.code === "unrecognized_keys") {
    return issue.keys.map(
      (key) => `${path === "" ? key : `${path}.${key}`}: unknown field`,
    );
  }
  return [`${path}: ${issue.message}`];
}

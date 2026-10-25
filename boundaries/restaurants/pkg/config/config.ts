import { loadEnvFile } from "node:process";
import { z } from "zod";

export const DEFAULT_PORT = 8082;
export const PORT_ENV = "SERVER__PORT";
export const DEFAULT_MANAGEMENT_PORT = 9082;
export const MANAGEMENT_PORT_ENV = "MANAGEMENT__PORT";
export const DEFAULT_READINESS_DELAY = "5s";
export const READINESS_DELAY_ENV = "SHUTDOWN__READINESS_DELAY";
export const DEFAULT_DRAIN_TIMEOUT = "20s";
export const DRAIN_TIMEOUT_ENV = "SHUTDOWN__DRAIN_TIMEOUT";
export const IDENTITY_ISSUER_ENV = "IDENTITY__ISSUER";
export const IDENTITY_AUDIENCE_ENV = "IDENTITY__AUDIENCE";
export const IDENTITY_JWKS_FILE_ENV = "IDENTITY__JWKS_FILE";
export const IDENTITY_SERVICE_ACCOUNTS_ENV = "IDENTITY__SERVICE_ACCOUNTS";
export const IDENTITY_USER_ISSUER_ENV = "IDENTITY__USER_ISSUER";
export const IDENTITY_USER_JWKS_FILE_ENV = "IDENTITY__USER_JWKS_FILE";
export const IDENTITY_USER_DELEGATES_ENV = "IDENTITY__USER_DELEGATES";
export const SPICEDB_ENDPOINT_ENV = "SPICEDB__ENDPOINT";
export const SPICEDB_PRESHARED_KEY_ENV = "SPICEDB__PRESHARED_KEY";
export const SPICEDB_INSECURE_ENV = "SPICEDB__INSECURE";
export const SPICEDB_TIMEOUT_ENV = "SPICEDB__TIMEOUT";
export const DEFAULT_SPICEDB_TIMEOUT = "2s";
export const MONGODB_URI_ENV = "MONGODB__URI";
export const MONGODB_DATABASE_ENV = "MONGODB__DATABASE";
export const DEFAULT_MONGODB_DATABASE = "restaurants";
export const MONGODB_TIMEOUT_ENV = "MONGODB__TIMEOUT";
export const DEFAULT_MONGODB_TIMEOUT = "5s";
export const RECONCILER_INTERVAL_ENV = "RECONCILER__INTERVAL";
export const DEFAULT_RECONCILER_INTERVAL = "5m";
export const RECONCILER_GRACE_ENV = "RECONCILER__GRACE";
export const DEFAULT_RECONCILER_GRACE = "10m";
export const RECONCILER_PAGE_SIZE_ENV = "RECONCILER__PAGE_SIZE";
export const DEFAULT_RECONCILER_PAGE_SIZE = 500;
export const KITCHEN_PREPARATION_TIME_ENV = "KITCHEN__PREPARATION_TIME";
export const DEFAULT_KITCHEN_PREPARATION_TIME = "20m";
export const KITCHEN_REPEAT_POLICY_ENV = "KITCHEN__REPEAT_POLICY";
export const DEFAULT_KITCHEN_REPEAT_POLICY = "succeed-without-event";
export const KITCHEN_CANCELLATION_DURING_PREPARATION_ENV =
  "KITCHEN__CANCELLATION_DURING_PREPARATION";
export const DEFAULT_KITCHEN_CANCELLATION_DURING_PREPARATION = "reject";

export const KAFKA_BROKERS_ENV = "KAFKA__BROKERS";
export const KAFKA_CLIENT_ID_ENV = "KAFKA__CLIENT_ID";
export const DEFAULT_KAFKA_CLIENT_ID = "restaurants-relay";
export const OUTBOX_RETENTION_ENV = "OUTBOX__RETENTION";
export const DEFAULT_OUTBOX_RETENTION = "7d";
export const OUTBOX_POLL_INTERVAL_ENV = "OUTBOX__POLL_INTERVAL";
export const DEFAULT_OUTBOX_POLL_INTERVAL = "1s";
export const OUTBOX_BATCH_SIZE_ENV = "OUTBOX__BATCH_SIZE";
export const DEFAULT_OUTBOX_BATCH_SIZE = 100;
export const OUTBOX_LEASE_TTL_ENV = "OUTBOX__LEASE_TTL";
export const DEFAULT_OUTBOX_LEASE_TTL = "15s";

const portSchema = z.coerce.number().int().min(1).max(65535);

const DURATION_UNITS_MS = {
  ms: 1,
  s: 1000,
  m: 60_000,
  h: 3_600_000,
  d: 86_400_000,
} as const;

/** Длительность вида `500ms`, `5s`, `1m`, `2h`, `7d` в миллисекундах. */
const durationSchema = z
  .string()
  .regex(
    /^\d+(ms|s|m|h|d)$/,
    "expected a duration like 500ms, 5s, 1m, 2h or 7d",
  )
  .transform((value) => {
    const [, amount, unit] = /^(\d+)(ms|s|m|h|d)$/.exec(value) ?? [];
    return (
      Number(amount) * DURATION_UNITS_MS[unit as keyof typeof DURATION_UNITS_MS]
    );
  });

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
}

/**
 * Проверка service identity по projected ServiceAccount token (ADR 27).
 * `enabled: false`, пока не заданы issuer, audience и JWKS: сервер остаётся
 * на `RejectingSubjectResolver` (fail closed).
 */
export type IdentityConfig =
  | Readonly<{ enabled: false }>
  | Readonly<{
      enabled: true;
      issuer: string;
      audience: string;
      jwksFile: string;
      /** `system:serviceaccount:<namespace>:<name>` → имя сервиса. */
      serviceAccounts: ReadonlyMap<string, string>;
      user: UserIdentityConfig;
    }>;

/**
 * Проверка user assertion BFF (ADR 32). `enabled: false`, пока не заданы
 * issuer, JWKS и непустой список делегатов: любой вызов с assertion
 * отклоняется, вызовы без него проверяются как раньше.
 */
export type UserIdentityConfig =
  | Readonly<{ enabled: false }>
  | Readonly<{
      enabled: true;
      issuer: string;
      jwksFile: string;
      /** Services, которым разрешено передавать user subject. */
      delegates: ReadonlySet<string>;
    }>;

/** Подключение к SpiceDB (ADR 18), когда задан `SPICEDB__ENDPOINT`. */
export type SpiceDbEnabledConfig = Readonly<{
  enabled: true;
  /** `host:port` gRPC API SpiceDB. */
  endpoint: string;
  /** Bearer preshared key; приходит из Secret, не из values. */
  presharedKey: string;
  /** Plaintext gRPC — только kind и локальный запуск. */
  insecure: boolean;
  /** Deadline одной проверки permission. */
  timeoutMs: number;
}>;

/**
 * `enabled: false` при пустом `SPICEDB__ENDPOINT`: авторизация остаётся на
 * in-memory адаптерах без grants (deny-all).
 */
export type SpiceDbConfig = Readonly<{ enabled: false }> | SpiceDbEnabledConfig;

/** Подключение к MongoDB (ADR 8, локальный ADR 1), когда задан `MONGODB__URI`. */
export type MongoDbEnabledConfig = Readonly<{
  enabled: true;
  /** Connection string replica set; приходит из Secret, не из values. */
  uri: string;
  database: string;
  /** Server selection timeout: недоступная MongoDB останавливает старт. */
  timeoutMs: number;
}>;

/**
 * `enabled: false` при пустом `MONGODB__URI`; процессы `restaurants` и
 * `restaurants-migrate` в этом случае не стартуют.
 */
export type MongoDbConfig = Readonly<{ enabled: false }> | MongoDbEnabledConfig;

/**
 * Background reconciliation of `restaurant#owner`/`staff` with the stored
 * restaurants (FR-445); runs inside the main process.
 */
export interface ReconcilerConfig {
  readonly intervalMs: number;
  /** An orphan relationship is deleted only after staying orphan this long. */
  readonly graceMs: number;
  /** ReadRelationships page size. */
  readonly pageSize: number;
}

/**
 * Open product decisions of Kitchen Order commands. Handlers take them as
 * required parameters without defaults; the defaults live here.
 */
export interface KitchenConfig {
  /**
   * Initial `expected_ready_at` after Acceptance; staff move it later with
   * Report Preparation Delay.
   */
  readonly preparationTimeMs: number;
  /**
   * A repeated Start Preparation or Mark Ready for Pickup: `succeed-without-event`
   * answers with the current order and publishes nothing, `reject` fails.
   */
  readonly repeatPolicy: "succeed-without-event" | "reject";
  /** Cancel Restaurant Order during Preparation: `reject` keeps it going. */
  readonly cancellationDuringPreparation: "allow" | "reject";
}

/**
 * Kafka брокеры relay (ADR 31). `enabled: false` при пустом `KAFKA__BROKERS`;
 * restaurants-relay в этом случае не стартует, основной процесс к Kafka не
 * подключается.
 */
export type KafkaConfig =
  | Readonly<{ enabled: false }>
  | Readonly<{ enabled: true; brokers: readonly string[]; clientId: string }>;

/** Polling relay Outbox (ADR 31). */
export interface OutboxRelayConfig {
  /** Опубликованные записи старше этого срока удаляются. */
  readonly retentionMs: number;
  readonly pollIntervalMs: number;
  readonly batchSize: number;
  /** Срок lease единственного активного relay; продлевается каждый цикл. */
  readonly leaseTtlMs: number;
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
    identity: loadIdentityConfig(environment),
    spicedb: loadSpiceDbConfig(environment),
    mongodb: loadMongoDbConfig(environment),
    reconciler: loadReconcilerConfig(environment),
    kitchen: loadKitchenConfig(environment),
    kafka: loadKafkaConfig(environment),
    outbox: loadOutboxRelayConfig(environment),
  });
}

const booleanSchema = z
  .enum(["true", "false"], { error: "expected true or false" })
  .transform((value) => value === "true");

export function loadSpiceDbConfig(
  environment: Record<string, string | undefined>,
): SpiceDbConfig {
  const endpoint = environment[SPICEDB_ENDPOINT_ENV]?.trim() ?? "";
  if (endpoint === "") {
    return Object.freeze({ enabled: false });
  }
  const presharedKey = environment[SPICEDB_PRESHARED_KEY_ENV]?.trim() ?? "";
  if (presharedKey === "") {
    throw new Error(
      `${SPICEDB_PRESHARED_KEY_ENV} must not be empty when ${SPICEDB_ENDPOINT_ENV} is set`,
    );
  }
  const insecure = booleanSchema.parse(
    environment[SPICEDB_INSECURE_ENV]?.trim() || "false",
  );
  const timeoutMs = durationSchema
    .refine((value) => value > 0, "SpiceDB timeout must be positive")
    .parse(environment[SPICEDB_TIMEOUT_ENV]?.trim() || DEFAULT_SPICEDB_TIMEOUT);
  return Object.freeze({
    enabled: true,
    endpoint,
    presharedKey,
    insecure,
    timeoutMs,
  });
}

const MONGODB_DATABASE_NAME = /^[A-Za-z0-9_-]{1,63}$/;

export function loadMongoDbConfig(
  environment: Record<string, string | undefined>,
): MongoDbConfig {
  const uri = environment[MONGODB_URI_ENV]?.trim() ?? "";
  if (uri === "") {
    return Object.freeze({ enabled: false });
  }
  if (!/^mongodb(\+srv)?:\/\//.test(uri)) {
    throw new Error(
      `${MONGODB_URI_ENV} must be a mongodb:// or mongodb+srv:// connection string`,
    );
  }
  const database =
    environment[MONGODB_DATABASE_ENV]?.trim() || DEFAULT_MONGODB_DATABASE;
  if (!MONGODB_DATABASE_NAME.test(database)) {
    throw new Error(
      `${MONGODB_DATABASE_ENV}: invalid database name "${database}"`,
    );
  }
  const timeoutMs = durationSchema
    .refine((value) => value > 0, "MongoDB timeout must be positive")
    .parse(environment[MONGODB_TIMEOUT_ENV]?.trim() || DEFAULT_MONGODB_TIMEOUT);
  return Object.freeze({ enabled: true, uri, database, timeoutMs });
}

export function loadReconcilerConfig(
  environment: Record<string, string | undefined>,
): ReconcilerConfig {
  const intervalMs = durationSchema
    .refine((value) => value > 0, "reconciler interval must be positive")
    .parse(
      environment[RECONCILER_INTERVAL_ENV]?.trim() ||
        DEFAULT_RECONCILER_INTERVAL,
    );
  const graceMs = durationSchema
    .refine((value) => value > 0, "reconciler grace must be positive")
    .parse(
      environment[RECONCILER_GRACE_ENV]?.trim() || DEFAULT_RECONCILER_GRACE,
    );
  // SpiceDB caps ReadRelationships at 1000 relationships per page by default.
  const pageSize = z.coerce
    .number()
    .int()
    .min(1)
    .max(1000)
    .parse(
      environment[RECONCILER_PAGE_SIZE_ENV]?.trim() ||
        DEFAULT_RECONCILER_PAGE_SIZE,
    );
  return Object.freeze({ intervalMs, graceMs, pageSize });
}

export function loadKitchenConfig(
  environment: Record<string, string | undefined>,
): KitchenConfig {
  const preparationTimeMs = durationSchema
    .refine((value) => value > 0, "preparation time must be positive")
    .parse(
      environment[KITCHEN_PREPARATION_TIME_ENV]?.trim() ||
        DEFAULT_KITCHEN_PREPARATION_TIME,
    );
  const repeatPolicy = z
    .enum(["succeed-without-event", "reject"], {
      error: `${KITCHEN_REPEAT_POLICY_ENV}: expected succeed-without-event or reject`,
    })
    .parse(
      environment[KITCHEN_REPEAT_POLICY_ENV]?.trim() ||
        DEFAULT_KITCHEN_REPEAT_POLICY,
    );
  const cancellationDuringPreparation = z
    .enum(["allow", "reject"], {
      error: `${KITCHEN_CANCELLATION_DURING_PREPARATION_ENV}: expected allow or reject`,
    })
    .parse(
      environment[KITCHEN_CANCELLATION_DURING_PREPARATION_ENV]?.trim() ||
        DEFAULT_KITCHEN_CANCELLATION_DURING_PREPARATION,
    );
  return Object.freeze({
    preparationTimeMs,
    repeatPolicy,
    cancellationDuringPreparation,
  });
}

const BROKER = /^[A-Za-z0-9._-]+:\d{1,5}$/;

export function loadKafkaConfig(
  environment: Record<string, string | undefined>,
): KafkaConfig {
  const raw = environment[KAFKA_BROKERS_ENV]?.trim() ?? "";
  if (raw === "") return Object.freeze({ enabled: false });
  const brokers = raw.split(",").map((broker) => broker.trim());
  for (const broker of brokers) {
    if (!BROKER.test(broker)) {
      throw new Error(
        `${KAFKA_BROKERS_ENV}: malformed broker "${broker}", expected host:port`,
      );
    }
  }
  const clientId =
    environment[KAFKA_CLIENT_ID_ENV]?.trim() || DEFAULT_KAFKA_CLIENT_ID;
  return Object.freeze({
    enabled: true,
    brokers: Object.freeze(brokers),
    clientId,
  });
}

export function loadOutboxRelayConfig(
  environment: Record<string, string | undefined>,
): OutboxRelayConfig {
  const positive = (name: string) =>
    durationSchema.refine((value) => value > 0, `${name} must be positive`);
  const retentionMs = positive(OUTBOX_RETENTION_ENV).parse(
    environment[OUTBOX_RETENTION_ENV]?.trim() || DEFAULT_OUTBOX_RETENTION,
  );
  const pollIntervalMs = positive(OUTBOX_POLL_INTERVAL_ENV).parse(
    environment[OUTBOX_POLL_INTERVAL_ENV]?.trim() ||
      DEFAULT_OUTBOX_POLL_INTERVAL,
  );
  const leaseTtlMs = positive(OUTBOX_LEASE_TTL_ENV).parse(
    environment[OUTBOX_LEASE_TTL_ENV]?.trim() || DEFAULT_OUTBOX_LEASE_TTL,
  );
  if (leaseTtlMs <= pollIntervalMs) {
    throw new Error(
      `${OUTBOX_LEASE_TTL_ENV} must be longer than ${OUTBOX_POLL_INTERVAL_ENV}`,
    );
  }
  const batchSize = z.coerce
    .number()
    .int()
    .min(1)
    .max(10_000)
    .parse(
      environment[OUTBOX_BATCH_SIZE_ENV]?.trim() || DEFAULT_OUTBOX_BATCH_SIZE,
    );
  return Object.freeze({ retentionMs, pollIntervalMs, batchSize, leaseTtlMs });
}

/** Сегмент namespace / ServiceAccount: DNS-1123 subdomain. */
const KUBERNETES_NAME = /^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$/;
const SERVICE_NAME = /^[A-Za-z0-9]([-A-Za-z0-9._]*[A-Za-z0-9])?$/;
const SERVICE_ACCOUNT_ENTRY = /^([^/=]*)\/([^/=]*)=([^/=]*)$/;

export function loadIdentityConfig(
  environment: Record<string, string | undefined>,
): IdentityConfig {
  const issuer = environment[IDENTITY_ISSUER_ENV]?.trim() ?? "";
  const audience = environment[IDENTITY_AUDIENCE_ENV]?.trim() ?? "";
  const jwksFile = environment[IDENTITY_JWKS_FILE_ENV]?.trim() ?? "";
  const serviceAccounts = parseServiceAccounts(
    environment[IDENTITY_SERVICE_ACCOUNTS_ENV] ?? "",
  );

  if (issuer !== "" && serviceAccounts.size === 0) {
    throw new Error(
      `${IDENTITY_SERVICE_ACCOUNTS_ENV} must not be empty when ${IDENTITY_ISSUER_ENV} is set`,
    );
  }
  if (issuer === "" || audience === "" || jwksFile === "") {
    return Object.freeze({ enabled: false });
  }
  return Object.freeze({
    enabled: true,
    issuer,
    audience,
    jwksFile,
    serviceAccounts,
    user: loadUserIdentityConfig(environment),
  });
}

function loadUserIdentityConfig(
  environment: Record<string, string | undefined>,
): UserIdentityConfig {
  const issuer = environment[IDENTITY_USER_ISSUER_ENV]?.trim() ?? "";
  const jwksFile = environment[IDENTITY_USER_JWKS_FILE_ENV]?.trim() ?? "";
  const delegates = parseDelegates(
    environment[IDENTITY_USER_DELEGATES_ENV] ?? "",
  );
  if (issuer === "" || jwksFile === "" || delegates.size === 0) {
    return Object.freeze({ enabled: false });
  }
  return Object.freeze({ enabled: true, issuer, jwksFile, delegates });
}

/** Разбирает имена services через запятую. */
function parseDelegates(value: string): ReadonlySet<string> {
  const delegates = new Set<string>();
  if (value.trim() === "") {
    return delegates;
  }
  for (const rawEntry of value.split(",")) {
    const entry = rawEntry.trim();
    if (!SERVICE_NAME.test(entry)) {
      throw new Error(
        `${IDENTITY_USER_DELEGATES_ENV}: malformed service name "${entry}"`,
      );
    }
    delegates.add(entry);
  }
  return delegates;
}

/** Разбирает `namespace/serviceaccount=service-name` через запятую. */
function parseServiceAccounts(value: string): ReadonlyMap<string, string> {
  const allowlist = new Map<string, string>();
  if (value.trim() === "") {
    return allowlist;
  }
  for (const rawEntry of value.split(",")) {
    const entry = rawEntry.trim();
    const match = SERVICE_ACCOUNT_ENTRY.exec(entry);
    const namespace = match?.[1]?.trim() ?? "";
    const name = match?.[2]?.trim() ?? "";
    const service = match?.[3]?.trim() ?? "";
    if (
      !KUBERNETES_NAME.test(namespace) ||
      !KUBERNETES_NAME.test(name) ||
      !SERVICE_NAME.test(service)
    ) {
      throw new Error(
        `${IDENTITY_SERVICE_ACCOUNTS_ENV}: malformed entry "${entry}", expected namespace/serviceaccount=service-name`,
      );
    }
    const subject = `system:serviceaccount:${namespace}:${name}`;
    const known = allowlist.get(subject);
    if (known !== undefined && known !== service) {
      throw new Error(
        `${IDENTITY_SERVICE_ACCOUNTS_ENV}: ${namespace}/${name} is mapped to both ${known} and ${service}`,
      );
    }
    allowlist.set(subject, service);
  }
  return allowlist;
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

  return Object.freeze({ readinessDelayMs, drainTimeoutMs });
}

function loadServerConfig(
  environment: Record<string, string | undefined>,
): ServerConfig {
  const port = portSchema.parse(
    environment[PORT_ENV] ?? environment.PORT ?? DEFAULT_PORT,
  );

  return Object.freeze({ port });
}

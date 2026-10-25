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

const portSchema = z.coerce.number().int().min(1).max(65535);

const DURATION_UNITS_MS = { ms: 1, s: 1000, m: 60_000 } as const;

/** Длительность вида `500ms`, `5s`, `1m` в миллисекундах. */
const durationSchema = z
  .string()
  .regex(/^\d+(ms|s|m)$/, "expected a duration like 500ms, 5s or 1m")
  .transform((value) => {
    const [, amount, unit] = /^(\d+)(ms|s|m)$/.exec(value) ?? [];
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
    }>;

export interface AppConfig {
  readonly server: ServerConfig;
  readonly management: ManagementConfig;
  readonly shutdown: ShutdownConfig;
  readonly identity: IdentityConfig;
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
  });
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
  });
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

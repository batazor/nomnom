import {
  IDENTITY_AUDIENCE_ENV,
  IDENTITY_ISSUER_ENV,
  IDENTITY_JWKS_FILE_ENV,
  IDENTITY_SERVICE_ACCOUNTS_ENV,
  IDENTITY_USER_DELEGATES_ENV,
  IDENTITY_USER_ISSUER_ENV,
  IDENTITY_USER_JWKS_FILE_ENV,
} from "./env.ts";

/**
 * Проверка service identity по projected ServiceAccount token (ADR 27) и user
 * assertions BFF (ADR 32). Issuer, audience, JWKS, allowlist и user-настройки
 * обязательны: пустое значение — ошибка старта процесса с gRPC API.
 */
export type IdentityConfig = Readonly<{
  issuer: string;
  audience: string;
  jwksFile: string;
  /** `system:serviceaccount:<namespace>:<name>` → имя сервиса. */
  serviceAccounts: ReadonlyMap<string, string>;
  user: UserIdentityConfig;
}>;

/** Проверка user assertion BFF (ADR 32). */
export type UserIdentityConfig = Readonly<{
  issuer: string;
  jwksFile: string;
  /** Services, которым разрешено передавать user subject. */
  delegates: ReadonlySet<string>;
}>;

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

  if (issuer === "" || audience === "" || jwksFile === "") {
    throw new Error(
      `${IDENTITY_ISSUER_ENV}, ${IDENTITY_AUDIENCE_ENV} and ${IDENTITY_JWKS_FILE_ENV} are required`,
    );
  }
  if (serviceAccounts.size === 0) {
    throw new Error(`${IDENTITY_SERVICE_ACCOUNTS_ENV} must not be empty`);
  }
  return Object.freeze({
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
  const missing = [
    issuer === "" ? IDENTITY_USER_ISSUER_ENV : "",
    jwksFile === "" ? IDENTITY_USER_JWKS_FILE_ENV : "",
    delegates.size === 0 ? IDENTITY_USER_DELEGATES_ENV : "",
  ].filter((name) => name !== "");
  if (missing.length > 0) {
    throw new Error(`${missing.join(", ")} must be set`);
  }
  return Object.freeze({ issuer, jwksFile, delegates });
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

import { z } from "zod";

import type { Spec } from "./file.ts";
import { optionalStringSchema } from "./schemas.ts";

/**
 * `spec.identity`: issuer, audience, путь JWKS и allowlist кластера, issuer,
 * путь JWKS и делегаты BFF. Пути JWKS в kind задаёт chart по смонтированным
 * ConfigMap.
 */
export const identitySectionSchema = z
  .strictObject({
    issuer: optionalStringSchema,
    audience: optionalStringSchema,
    jwksFile: optionalStringSchema,
    /** `namespace/serviceaccount=service-name` через запятую. */
    serviceAccounts: optionalStringSchema,
    userIssuer: optionalStringSchema,
    userJwksFile: optionalStringSchema,
    /** Имена services через запятую. */
    userDelegates: optionalStringSchema,
  })
  .prefault({});

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
  spec: Pick<Spec, "identity">,
): IdentityConfig {
  const { identity } = spec;
  const issuer = identity.issuer ?? "";
  const audience = identity.audience ?? "";
  const jwksFile = identity.jwksFile ?? "";
  const serviceAccounts = parseServiceAccounts(identity.serviceAccounts ?? "");

  if (issuer === "" || audience === "" || jwksFile === "") {
    throw new Error(
      "spec.identity.issuer, spec.identity.audience and spec.identity.jwksFile are required",
    );
  }
  if (serviceAccounts.size === 0) {
    throw new Error("spec.identity.serviceAccounts must not be empty");
  }
  return Object.freeze({
    issuer,
    audience,
    jwksFile,
    serviceAccounts,
    user: loadUserIdentityConfig(identity),
  });
}

function loadUserIdentityConfig(
  identity: Spec["identity"],
): UserIdentityConfig {
  const issuer = identity.userIssuer ?? "";
  const jwksFile = identity.userJwksFile ?? "";
  const delegates = parseDelegates(identity.userDelegates ?? "");
  const missing = [
    issuer === "" ? "spec.identity.userIssuer" : "",
    jwksFile === "" ? "spec.identity.userJwksFile" : "",
    delegates.size === 0 ? "spec.identity.userDelegates" : "",
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
        `spec.identity.userDelegates: malformed service name "${entry}"`,
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
        `spec.identity.serviceAccounts: malformed entry "${entry}", expected namespace/serviceaccount=service-name`,
      );
    }
    const subject = `system:serviceaccount:${namespace}:${name}`;
    const known = allowlist.get(subject);
    if (known !== undefined && known !== service) {
      throw new Error(
        `spec.identity.serviceAccounts: ${namespace}/${name} is mapped to both ${known} and ${service}`,
      );
    }
    allowlist.set(subject, service);
  }
  return allowlist;
}

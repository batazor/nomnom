import { Code, ConnectError, type HandlerContext } from "@connectrpc/connect";
import { decodeJwt, type JSONWebKeySet } from "jose";

import type { Subject } from "../../pkg/application/subject.ts";
import type { IdentityConfig } from "../../pkg/config/config.ts";
import { readJwks, readUserJwks } from "./jwks.ts";
import { ServiceTokenVerifier } from "./service-token.ts";
import {
  USER_ASSERTION_HEADER,
  type UserAssertionOptions,
  UserAssertionVerifier,
} from "./user-assertion.ts";

export { readJwks, readUserJwks } from "./jwks.ts";
export {
  USER_ASSERTION_HEADER,
  type UserAssertionOptions,
} from "./user-assertion.ts";

/**
 * Authenticates the transport caller and returns the verified application
 * identity. Implementations, not RPC messages, own credentials.
 */
export interface SubjectResolver {
  resolve(context: HandlerContext): Promise<Subject>;
}

const MAX_LOGGED_JTI_LENGTH = 128;

export interface ServiceTokenSubjectResolverOptions {
  readonly issuer: string;
  readonly audience: string;
  readonly jwks: JSONWebKeySet;
  /** `system:serviceaccount:<namespace>:<name>` → имя сервиса. */
  readonly serviceAccounts: ReadonlyMap<string, string>;
  /** Получает причину отказа; клиенту она не передаётся. */
  readonly log?: (message: string) => void;
  /** Текущее время для проверки `exp`/`nbf`; по умолчанию системное. */
  readonly now?: () => Date;
  /** Проверка user assertions BFF (ADR 32). */
  readonly userAssertions: UserAssertionOptions;
  /** Получает принятые user assertions (delegate и `jti`, без токенов). */
  readonly audit?: (message: string) => void;
}

/**
 * Resolves the caller from `authorization: Bearer <jwt>` with
 * {@link ServiceTokenVerifier}. When the call also carries
 * `x-nomnom-user-assertion`, {@link UserAssertionVerifier} turns the
 * authenticated delegate's assertion into a `user` subject (ADR 32). The
 * resolver only orders the two checks, logs and audits; every failure is
 * UNAUTHENTICATED without details.
 */
export class ServiceTokenSubjectResolver implements SubjectResolver {
  readonly #services: ServiceTokenVerifier;
  readonly #users: UserAssertionVerifier;
  readonly #log: (message: string) => void;
  readonly #audit: (message: string) => void;

  constructor(options: ServiceTokenSubjectResolverOptions) {
    const now = options.now ?? (() => new Date());
    this.#services = new ServiceTokenVerifier({
      issuer: options.issuer,
      audience: options.audience,
      jwks: options.jwks,
      serviceAccounts: options.serviceAccounts,
      now,
    });
    this.#users = new UserAssertionVerifier({
      ...options.userAssertions,
      audience: options.audience,
      now,
    });
    this.#log = options.log ?? (() => {});
    this.#audit = options.audit ?? (() => {});
  }

  async resolve(context: HandlerContext): Promise<Subject> {
    let service: Subject;
    try {
      service = await this.#services.verify(
        context.requestHeader.get("authorization"),
      );
    } catch (error) {
      this.#log(`service identity rejected: ${reasonOf(error)}`);
      throw unauthenticated();
    }
    const assertion = context.requestHeader.get(USER_ASSERTION_HEADER);
    if (assertion === null) {
      return service;
    }
    try {
      const user = await this.#users.verify(service.id, assertion);
      this.#audit(
        `user assertion accepted: delegate=${service.id} jti=${loggedJti(assertion)}`,
      );
      return user;
    } catch (error) {
      this.#log(
        `user assertion rejected: delegate=${service.id} jti=${loggedJti(assertion)}: ${reasonOf(error)}`,
      );
      throw unauthenticated();
    }
  }
}

const unauthenticated = () =>
  new ConnectError(
    "verified caller identity is required",
    Code.Unauthenticated,
  );

const reasonOf = (error: unknown) =>
  error instanceof Error ? error.message : String(error);

/** `jti` непроверенного assertion только для log; сам токен не пишется. */
function loggedJti(assertion: string): string {
  try {
    const { jti } = decodeJwt(assertion);
    return typeof jti === "string"
      ? JSON.stringify(jti.slice(0, MAX_LOGGED_JTI_LENGTH))
      : "-";
  } catch {
    return "-";
  }
}

export interface CreateSubjectResolverOptions {
  /** Принятые user assertions; по умолчанию не пишутся. */
  readonly audit?: (message: string) => void;
  /** Текущее время для проверки токенов; по умолчанию системное. */
  readonly now?: () => Date;
}

/**
 * Builds the resolver from configuration. Issuer, audience, JWKS and the user
 * assertion settings are required; their absence or an unreadable or invalid
 * JWKS (cluster or BFF) is a startup error.
 */
export async function createSubjectResolver(
  identity: IdentityConfig,
  log: (message: string) => void = () => {},
  options: CreateSubjectResolverOptions = {},
): Promise<SubjectResolver> {
  if (!identity.enabled) {
    throw new Error(
      "IDENTITY__ISSUER, IDENTITY__AUDIENCE and IDENTITY__JWKS_FILE are required",
    );
  }
  return new ServiceTokenSubjectResolver({
    issuer: identity.issuer,
    audience: identity.audience,
    jwks: await readJwks(identity.jwksFile),
    serviceAccounts: identity.serviceAccounts,
    log,
    audit: options.audit,
    now: options.now,
    userAssertions: {
      issuer: identity.user.issuer,
      jwks: await readUserJwks(identity.user.jwksFile),
      delegates: identity.user.delegates,
    },
  });
}

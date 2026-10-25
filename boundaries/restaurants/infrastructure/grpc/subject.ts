import { Code, ConnectError, type HandlerContext } from "@connectrpc/connect";
import { decodeJwt, type JSONWebKeySet } from "jose";

import type { Subject } from "../../pkg/application/subject.ts";
import type {
  IdentityConfig,
  UserIdentityConfig,
} from "../../pkg/config/config.ts";
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

/**
 * Secure default until the internal identity verification adapter is
 * configured. It deliberately does not trust arbitrary gRPC metadata or the
 * fact that a request passed through the gateway.
 */
export class RejectingSubjectResolver implements SubjectResolver {
  async resolve(): Promise<Subject> {
    throw new ConnectError(
      "verified caller identity is required",
      Code.Unauthenticated,
    );
  }
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
  /** Без настроек любой вызов с user assertion — UNAUTHENTICATED. */
  readonly userAssertions?: UserAssertionOptions;
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
  readonly #users: UserAssertionVerifier | undefined;
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
    const user = options.userAssertions;
    this.#users =
      user === undefined
        ? undefined
        : new UserAssertionVerifier({
            ...user,
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
      if (this.#users === undefined) {
        throw new Error("user assertions are not configured");
      }
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
 * Builds the resolver from configuration. Without issuer, audience and JWKS
 * the server stays fail closed; an unreadable or invalid JWKS (cluster or
 * BFF) is a startup error. Without user settings assertions are rejected.
 */
export async function createSubjectResolver(
  identity: IdentityConfig,
  log: (message: string) => void = () => {},
  options: CreateSubjectResolverOptions = {},
): Promise<SubjectResolver> {
  if (!identity.enabled) {
    log(
      "service identity is not configured (IDENTITY__ISSUER, IDENTITY__AUDIENCE, IDENTITY__JWKS_FILE); all calls requiring a subject are rejected",
    );
    return new RejectingSubjectResolver();
  }
  return new ServiceTokenSubjectResolver({
    issuer: identity.issuer,
    audience: identity.audience,
    jwks: await readJwks(identity.jwksFile),
    serviceAccounts: identity.serviceAccounts,
    log,
    audit: options.audit,
    now: options.now,
    userAssertions: await userAssertionOptions(identity.user, log),
  });
}

async function userAssertionOptions(
  user: UserIdentityConfig,
  log: (message: string) => void,
): Promise<UserAssertionOptions | undefined> {
  if (!user.enabled) {
    log(
      "user assertions are not configured (IDENTITY__USER_ISSUER, IDENTITY__USER_JWKS_FILE, IDENTITY__USER_DELEGATES); calls carrying x-nomnom-user-assertion are rejected",
    );
    return undefined;
  }
  return {
    issuer: user.issuer,
    jwks: await readUserJwks(user.jwksFile),
    delegates: user.delegates,
  };
}

import { Code, ConnectError, type HandlerContext } from "@connectrpc/connect";
import {
  createLocalJWKSet,
  decodeJwt,
  decodeProtectedHeader,
  type JSONWebKeySet,
  jwtVerify,
} from "jose";

import type { Subject } from "../../pkg/application/subject.ts";
import type {
  IdentityConfig,
  UserIdentityConfig,
} from "../../pkg/config/config.ts";
import {
  readJwks,
  readUserJwks,
  SERVICE_TOKEN_ALGORITHMS,
  USER_ASSERTION_ALGORITHMS,
} from "./jwks.ts";

export { readJwks, readUserJwks } from "./jwks.ts";

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

const CLOCK_TOLERANCE_SECONDS = 30;
const BEARER = /^Bearer +(\S+)$/i;

/** gRPC metadata с user assertion BFF (ADR 32). */
export const USER_ASSERTION_HEADER = "x-nomnom-user-assertion";
/** Предел `exp - iat`: защита от долгоживущих assertions при ошибке минтера. */
const USER_ASSERTION_MAX_LIFETIME_SECONDS = 60;
const MAX_LOGGED_JTI_LENGTH = 128;

export interface UserAssertionOptions {
  /** Ожидаемый `iss` assertion, `IDENTITY__USER_ISSUER`. */
  readonly issuer: string;
  /** JWKS BFF (EC P-256). */
  readonly jwks: JSONWebKeySet;
  /** Services, которым разрешено передавать user subject. */
  readonly delegates: ReadonlySet<string>;
}

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

type UserAssertionVerifier = Readonly<{
  issuer: string;
  keys: ReturnType<typeof createLocalJWKSet>;
  delegates: ReadonlySet<string>;
}>;

/**
 * Verifies a projected ServiceAccount token from `authorization: Bearer <jwt>`
 * against the cluster JWKS and maps an allowlisted ServiceAccount to a
 * `service` subject. When the call also carries `x-nomnom-user-assertion`,
 * the authenticated service must be an allowed delegate and the BFF-signed
 * assertion yields a `user` subject (ADR 32). Every failure is
 * UNAUTHENTICATED without details.
 */
export class ServiceTokenSubjectResolver implements SubjectResolver {
  readonly #issuer: string;
  readonly #audience: string;
  readonly #keys: ReturnType<typeof createLocalJWKSet>;
  readonly #serviceAccounts: ReadonlyMap<string, string>;
  readonly #log: (message: string) => void;
  readonly #now: () => Date;
  readonly #user: UserAssertionVerifier | undefined;
  readonly #audit: (message: string) => void;

  constructor(options: ServiceTokenSubjectResolverOptions) {
    this.#issuer = options.issuer;
    this.#audience = options.audience;
    this.#keys = createLocalJWKSet(options.jwks);
    this.#serviceAccounts = options.serviceAccounts;
    this.#log = options.log ?? ((message) => console.warn(message));
    this.#now = options.now ?? (() => new Date());
    this.#audit = options.audit ?? ((message) => console.info(message));
    const user = options.userAssertions;
    this.#user =
      user === undefined
        ? undefined
        : Object.freeze({
            issuer: user.issuer,
            keys: createLocalJWKSet(user.jwks),
            delegates: user.delegates,
          });
  }

  async resolve(context: HandlerContext): Promise<Subject> {
    let service: Subject;
    try {
      service = await this.#verify(context.requestHeader.get("authorization"));
    } catch (error) {
      this.#log(`service identity rejected: ${reasonOf(error)}`);
      throw unauthenticated();
    }
    const assertion = context.requestHeader.get(USER_ASSERTION_HEADER);
    if (assertion === null) {
      return service;
    }
    try {
      const user = await this.#verifyUser(service.id, assertion);
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

  async #verifyUser(delegate: string, assertion: string): Promise<Subject> {
    if (this.#user === undefined) {
      throw new Error("user assertions are not configured");
    }
    // Headers склеивает повторённые metadata через ", "; compact JWT запятых
    // не содержит.
    if (assertion === "" || assertion.includes(",")) {
      throw new Error(
        `${USER_ASSERTION_HEADER} must appear exactly once and be non-empty`,
      );
    }
    if (!this.#user.delegates.has(delegate)) {
      throw new Error(`service ${delegate} is not a user delegate`);
    }
    if (decodeProtectedHeader(assertion).kid === undefined) {
      throw new Error("assertion header has no kid");
    }
    const { payload } = await jwtVerify(assertion, this.#user.keys, {
      algorithms: USER_ASSERTION_ALGORITHMS,
      issuer: this.#user.issuer,
      audience: this.#audience,
      clockTolerance: CLOCK_TOLERANCE_SECONDS,
      currentDate: this.#now(),
      requiredClaims: ["exp", "iat", "sub", "jti"],
    });
    const { exp, iat, sub, jti } = payload;
    if (
      exp === undefined ||
      iat === undefined ||
      exp - iat > USER_ASSERTION_MAX_LIFETIME_SECONDS
    ) {
      throw new Error(
        `assertion lifetime exceeds ${USER_ASSERTION_MAX_LIFETIME_SECONDS}s`,
      );
    }
    if (typeof sub !== "string" || sub === "" || sub.trim() !== sub) {
      throw new Error("assertion sub is empty or padded");
    }
    if (typeof jti !== "string" || jti === "") {
      throw new Error("assertion jti is empty");
    }
    return Object.freeze({ type: "user", id: sub, delegate });
  }

  async #verify(authorization: string | null): Promise<Subject> {
    if (authorization === null) {
      throw new Error("authorization metadata is missing");
    }
    const token = BEARER.exec(authorization.trim())?.[1];
    if (token === undefined) {
      throw new Error("authorization metadata is not a Bearer token");
    }
    if (decodeProtectedHeader(token).kid === undefined) {
      throw new Error("token header has no kid");
    }
    const { payload } = await jwtVerify(token, this.#keys, {
      algorithms: SERVICE_TOKEN_ALGORITHMS,
      issuer: this.#issuer,
      audience: this.#audience,
      clockTolerance: CLOCK_TOLERANCE_SECONDS,
      currentDate: this.#now(),
      requiredClaims: ["exp", "sub"],
    });
    const service =
      payload.sub === undefined
        ? undefined
        : this.#serviceAccounts.get(payload.sub);
    if (service === undefined) {
      throw new Error(`service account ${payload.sub} is not allowlisted`);
    }
    return Object.freeze({ type: "service", id: service });
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
  /** Принятые user assertions; по умолчанию `console.info`. */
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
  log: (message: string) => void = (message) => console.warn(message),
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

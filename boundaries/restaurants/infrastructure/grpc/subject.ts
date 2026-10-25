import { readFile } from "node:fs/promises";

import { Code, ConnectError, type HandlerContext } from "@connectrpc/connect";
import {
  createLocalJWKSet,
  decodeProtectedHeader,
  importJWK,
  type JSONWebKeySet,
  jwtVerify,
} from "jose";

import type { Subject } from "../../pkg/application/subject.ts";
import type { IdentityConfig } from "../../pkg/config/config.ts";

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

/** Алгоритмы подписи projected ServiceAccount token (ADR 27). */
const SERVICE_TOKEN_ALGORITHMS = ["RS256", "ES256"];
const CLOCK_TOLERANCE_SECONDS = 30;
const BEARER = /^Bearer +(\S+)$/i;

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
}

/**
 * Verifies a projected ServiceAccount token from `authorization: Bearer <jwt>`
 * against the cluster JWKS and maps an allowlisted ServiceAccount to a
 * `service` subject. Every failure is UNAUTHENTICATED without details.
 */
export class ServiceTokenSubjectResolver implements SubjectResolver {
  readonly #issuer: string;
  readonly #audience: string;
  readonly #keys: ReturnType<typeof createLocalJWKSet>;
  readonly #serviceAccounts: ReadonlyMap<string, string>;
  readonly #log: (message: string) => void;
  readonly #now: () => Date;

  constructor(options: ServiceTokenSubjectResolverOptions) {
    this.#issuer = options.issuer;
    this.#audience = options.audience;
    this.#keys = createLocalJWKSet(options.jwks);
    this.#serviceAccounts = options.serviceAccounts;
    this.#log = options.log ?? ((message) => console.warn(message));
    this.#now = options.now ?? (() => new Date());
  }

  async resolve(context: HandlerContext): Promise<Subject> {
    try {
      return await this.#verify(context.requestHeader.get("authorization"));
    } catch (error) {
      const reason = error instanceof Error ? error.message : String(error);
      this.#log(`service identity rejected: ${reason}`);
      throw new ConnectError(
        "verified caller identity is required",
        Code.Unauthenticated,
      );
    }
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

/**
 * Builds the resolver from configuration. Without issuer, audience and JWKS
 * the server stays fail closed; an unreadable or invalid JWKS is a startup
 * error.
 */
export async function createSubjectResolver(
  identity: IdentityConfig,
  log: (message: string) => void = (message) => console.warn(message),
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
  });
}

/** Читает JWKS кластера и проверяет, что каждый ключ импортируется. */
export async function readJwks(path: string): Promise<JSONWebKeySet> {
  let jwks: JSONWebKeySet;
  try {
    jwks = JSON.parse(await readFile(path, "utf8")) as JSONWebKeySet;
  } catch (error) {
    throw new Error(`cannot read JWKS from ${path}`, { cause: error });
  }
  if (!Array.isArray(jwks?.keys) || jwks.keys.length === 0) {
    throw new Error(`JWKS ${path} has no keys`);
  }
  for (const key of jwks.keys) {
    if (typeof key.kid !== "string" || key.kid === "") {
      throw new Error(`JWKS ${path} has a key without kid`);
    }
    const algorithm = key.alg ?? (key.kty === "EC" ? "ES256" : "RS256");
    if (!SERVICE_TOKEN_ALGORITHMS.includes(algorithm)) {
      throw new Error(`JWKS ${path} key ${key.kid} uses ${algorithm}`);
    }
    try {
      await importJWK(key, algorithm);
    } catch (error) {
      throw new Error(`JWKS ${path} key ${key.kid} is invalid`, {
        cause: error,
      });
    }
  }
  createLocalJWKSet(jwks);
  return jwks;
}

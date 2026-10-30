import {
  createLocalJWKSet,
  decodeProtectedHeader,
  type JSONWebKeySet,
  jwtVerify,
} from "jose";

import type { Subject } from "../../pkg/authz/subject.ts";
import { CLOCK_TOLERANCE_SECONDS, SERVICE_TOKEN_ALGORITHMS } from "./jwks.ts";

const BEARER = /^Bearer +(\S+)$/i;

export interface ServiceTokenVerifierOptions {
  readonly issuer: string;
  readonly audience: string;
  readonly jwks: JSONWebKeySet;
  /** `system:serviceaccount:<namespace>:<name>` → имя сервиса. */
  readonly serviceAccounts: ReadonlyMap<string, string>;
  /** Текущее время для проверки `exp`/`nbf`; по умолчанию системное. */
  readonly now?: () => Date;
}

/**
 * Verifies a projected ServiceAccount token from `authorization: Bearer <jwt>`
 * against the cluster JWKS and maps an allowlisted ServiceAccount to a
 * `service` subject (ADR 27). Throws an error with the reason on any failure.
 */
export class ServiceTokenVerifier {
  readonly #issuer: string;
  readonly #audience: string;
  readonly #keys: ReturnType<typeof createLocalJWKSet>;
  readonly #serviceAccounts: ReadonlyMap<string, string>;
  readonly #now: () => Date;

  constructor(options: ServiceTokenVerifierOptions) {
    this.#issuer = options.issuer;
    this.#audience = options.audience;
    this.#keys = createLocalJWKSet(options.jwks);
    this.#serviceAccounts = options.serviceAccounts;
    this.#now = options.now ?? (() => new Date());
  }

  async verify(authorization: string | null): Promise<Subject> {
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

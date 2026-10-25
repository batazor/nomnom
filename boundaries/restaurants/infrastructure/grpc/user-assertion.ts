import {
  createLocalJWKSet,
  decodeProtectedHeader,
  type JSONWebKeySet,
  jwtVerify,
} from "jose";

import type { Subject } from "../../pkg/application/subject.ts";
import { CLOCK_TOLERANCE_SECONDS, USER_ASSERTION_ALGORITHMS } from "./jwks.ts";

/** gRPC metadata с user assertion BFF (ADR 32). */
export const USER_ASSERTION_HEADER = "x-nomnom-user-assertion";
/** Предел `exp - iat`: защита от долгоживущих assertions при ошибке минтера. */
const USER_ASSERTION_MAX_LIFETIME_SECONDS = 60;

export interface UserAssertionOptions {
  /** Ожидаемый `iss` assertion, `IDENTITY__USER_ISSUER`. */
  readonly issuer: string;
  /** JWKS BFF (EC P-256). */
  readonly jwks: JSONWebKeySet;
  /** Services, которым разрешено передавать user subject. */
  readonly delegates: ReadonlySet<string>;
}

export interface UserAssertionVerifierOptions extends UserAssertionOptions {
  /** Audience assertion: тот же, что у service token. */
  readonly audience: string;
  /** Текущее время для проверки `exp`/`nbf`; по умолчанию системное. */
  readonly now?: () => Date;
}

/**
 * Verifies a BFF-signed user assertion delegated by an authenticated service
 * and returns the `user` subject with the delegate kept for audit (ADR 32).
 * Throws an error with the reason on any failure.
 */
export class UserAssertionVerifier {
  readonly #issuer: string;
  readonly #audience: string;
  readonly #keys: ReturnType<typeof createLocalJWKSet>;
  readonly #delegates: ReadonlySet<string>;
  readonly #now: () => Date;

  constructor(options: UserAssertionVerifierOptions) {
    this.#issuer = options.issuer;
    this.#audience = options.audience;
    this.#keys = createLocalJWKSet(options.jwks);
    this.#delegates = options.delegates;
    this.#now = options.now ?? (() => new Date());
  }

  async verify(delegate: string, assertion: string): Promise<Subject> {
    // Headers склеивает повторённые metadata через ", "; compact JWT запятых
    // не содержит.
    if (assertion === "" || assertion.includes(",")) {
      throw new Error(
        `${USER_ASSERTION_HEADER} must appear exactly once and be non-empty`,
      );
    }
    if (!this.#delegates.has(delegate)) {
      throw new Error(`service ${delegate} is not a user delegate`);
    }
    if (decodeProtectedHeader(assertion).kid === undefined) {
      throw new Error("assertion header has no kid");
    }
    const { payload } = await jwtVerify(assertion, this.#keys, {
      algorithms: USER_ASSERTION_ALGORITHMS,
      issuer: this.#issuer,
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
}

import { readFile } from "node:fs/promises";

import { createLocalJWKSet, importJWK, type JSONWebKeySet } from "jose";

/** Алгоритмы подписи projected ServiceAccount token (ADR 27). */
export const SERVICE_TOKEN_ALGORITHMS = ["RS256", "ES256"];
/** Единственный алгоритм user assertion (ADR 32). */
export const USER_ASSERTION_ALGORITHMS = ["ES256"];
/** Допуск расхождения часов при проверке `exp`/`nbf`/`iat`. */
export const CLOCK_TOLERANCE_SECONDS = 30;

/** Читает JWKS BFF: только EC P-256 ключи для ES256 (ADR 32). */
export async function readUserJwks(path: string): Promise<JSONWebKeySet> {
  const jwks = await readJwks(path, USER_ASSERTION_ALGORITHMS);
  for (const key of jwks.keys) {
    if (key.kty !== "EC" || key.crv !== "P-256") {
      throw new Error(`JWKS ${path} key ${key.kid} is not an EC P-256 key`);
    }
  }
  return jwks;
}

/** Читает JWKS кластера и проверяет, что каждый ключ импортируется. */
export async function readJwks(
  path: string,
  algorithms: readonly string[] = SERVICE_TOKEN_ALGORITHMS,
): Promise<JSONWebKeySet> {
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
    if (!algorithms.includes(algorithm)) {
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

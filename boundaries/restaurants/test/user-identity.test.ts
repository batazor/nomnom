import assert from "node:assert/strict";
import { mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { Code, ConnectError, type HandlerContext } from "@connectrpc/connect";
import type { JSONWebKeySet } from "jose";

import {
  createSubjectResolver,
  ServiceTokenSubjectResolver,
  USER_ASSERTION_HEADER,
} from "../infrastructure/grpc/subject.ts";
import { loadIdentityConfig } from "../pkg/config/config.ts";

/** Общие тестовые векторы ADR 27 и ADR 32: `<repo>/tests/identity/fixtures`. */
const FIXTURES = new URL("../../../tests/identity/fixtures/", import.meta.url);
const JWKS_FILE = new URL("jwks.json", FIXTURES).pathname;
const BFF_JWKS_FILE = new URL("bff-jwks.json", FIXTURES).pathname;

type UserVectors = Readonly<{
  issuer: string;
  audience: string;
  now: number;
  delegates: string;
  service_issuer: string;
  service_accounts: string;
  delegate_token: string;
  non_delegate_token: string;
  expected_subject: string;
  accepted: readonly string[];
  rejected: readonly string[];
  tokens: Readonly<Record<string, string>>;
}>;

const vectors = JSON.parse(
  await readFile(new URL("user-vectors.json", FIXTURES), "utf8"),
) as UserVectors;
const clusterJwks = JSON.parse(
  await readFile(JWKS_FILE, "utf8"),
) as JSONWebKeySet;
const bffJwks = JSON.parse(
  await readFile(BFF_JWKS_FILE, "utf8"),
) as JSONWebKeySet;

/** Assertion живёт 30 s: часы зафиксированы на `now` векторов. */
const now = () => new Date(vectors.now * 1000);

const serviceEnvironment = {
  IDENTITY__ISSUER: vectors.service_issuer,
  IDENTITY__AUDIENCE: vectors.audience,
  IDENTITY__JWKS_FILE: JWKS_FILE,
  IDENTITY__SERVICE_ACCOUNTS: vectors.service_accounts,
};

const userEnvironment = {
  ...serviceEnvironment,
  IDENTITY__USER_ISSUER: vectors.issuer,
  IDENTITY__USER_JWKS_FILE: BFF_JWKS_FILE,
  IDENTITY__USER_DELEGATES: vectors.delegates,
};

const silent = () => {};

const createResolver = (
  environment: Record<string, string> = userEnvironment,
  log: (message: string) => void = silent,
  audit: (message: string) => void = silent,
) =>
  createSubjectResolver(loadIdentityConfig(environment), log, { audit, now });

const call = (serviceToken: string, assertion?: string): HandlerContext => {
  const headers = new Headers({ authorization: `Bearer ${serviceToken}` });
  if (assertion !== undefined) headers.append(USER_ASSERTION_HEADER, assertion);
  return { requestHeader: headers } as unknown as HandlerContext;
};

const unauthenticated = (error: unknown) =>
  error instanceof ConnectError &&
  error.code === Code.Unauthenticated &&
  error.rawMessage === "verified caller identity is required";

const valid = vectors.tokens.valid ?? "";

test("accepted assertions from the delegate resolve to the user subject (AC-525)", async () => {
  const accepted: string[] = [];
  const resolver = await createResolver(userEnvironment, silent, (message) =>
    accepted.push(message),
  );
  assert.ok(resolver instanceof ServiceTokenSubjectResolver);
  assert.deepEqual(vectors.accepted, ["valid", "valid_string_aud"]);
  assert.equal(vectors.expected_subject, "customer-1");

  for (const name of vectors.accepted) {
    const assertion = vectors.tokens[name];
    assert.ok(assertion, name);
    assert.deepEqual(
      await resolver.resolve(call(vectors.delegate_token, assertion)),
      { type: "user", id: vectors.expected_subject, delegate: "bff" },
      name,
    );
  }
  assert.equal(accepted.length, vectors.accepted.length);
  for (const message of accepted) {
    assert.match(message, /delegate=bff jti="0b6f1c1e-/);
    assert.ok(!message.includes(valid));
  }
});

test("rejected assertions are UNAUTHENTICATED (FR-472, AC-527, AC-528)", async () => {
  const reasons: string[] = [];
  const resolver = await createResolver(userEnvironment, (message) =>
    reasons.push(message),
  );
  assert.ok(vectors.rejected.length > 0);

  for (const name of vectors.rejected) {
    const assertion = vectors.tokens[name];
    assert.ok(assertion, name);
    await assert.rejects(
      resolver.resolve(call(vectors.delegate_token, assertion)),
      unauthenticated,
      name,
    );
  }
  assert.equal(reasons.length, vectors.rejected.length);
  for (const [index, reason] of reasons.entries()) {
    assert.match(reason, /^user assertion rejected: delegate=bff /);
    assert.ok(
      !reason.includes(vectors.tokens[vectors.rejected[index] ?? ""] ?? "-"),
    );
  }
});

test("a valid assertion from a non-delegate service is UNAUTHENTICATED (AC-526)", async () => {
  const reasons: string[] = [];
  const resolver = await createResolver(userEnvironment, (message) =>
    reasons.push(message),
  );

  await assert.rejects(
    resolver.resolve(call(vectors.non_delegate_token, valid)),
    unauthenticated,
  );
  assert.deepEqual(await resolver.resolve(call(vectors.non_delegate_token)), {
    type: "service",
    id: "orders-worker",
  });
  assert.equal(reasons.length, 1);
  assert.match(
    reasons[0] ?? "",
    /delegate=orders-worker .*not a user delegate/,
  );
});

test("an assertion without a valid service token is UNAUTHENTICATED (FR-471)", async () => {
  const resolver = await createResolver();
  const headers = new Headers({ [USER_ASSERTION_HEADER]: valid });

  await assert.rejects(
    resolver.resolve({ requestHeader: headers } as unknown as HandlerContext),
    unauthenticated,
  );
  await assert.rejects(
    resolver.resolve(call(vectors.tokens.bad_signature ?? "", valid)),
    unauthenticated,
  );
});

test("a repeated or empty assertion is UNAUTHENTICATED", async () => {
  const resolver = await createResolver();
  const repeated = call(vectors.delegate_token, valid);
  repeated.requestHeader.append(USER_ASSERTION_HEADER, valid);

  await assert.rejects(resolver.resolve(repeated), unauthenticated);
  await assert.rejects(
    resolver.resolve(call(vectors.delegate_token, "")),
    unauthenticated,
  );
  await assert.rejects(
    resolver.resolve(call(vectors.delegate_token, "   ")),
    unauthenticated,
  );
});

test("assertion expiry and not-before honour the 30s clock tolerance", async () => {
  const at = (seconds: number) =>
    new ServiceTokenSubjectResolver({
      issuer: vectors.service_issuer,
      audience: vectors.audience,
      jwks: clusterJwks,
      serviceAccounts: new Map([["system:serviceaccount:nomnom:bff", "bff"]]),
      log: silent,
      audit: silent,
      now: () => new Date(seconds * 1000),
      userAssertions: {
        issuer: vectors.issuer,
        jwks: bffJwks,
        delegates: new Set(["bff"]),
      },
    });
  const context = call(vectors.delegate_token, valid);
  // valid: nbf = iat = 1700000000, exp = 1700000030.
  await at(1_700_000_000 - 29).resolve(context);
  await assert.rejects(
    at(1_700_000_000 - 31).resolve(context),
    unauthenticated,
  );
  await at(1_700_000_030 + 29).resolve(context);
  await assert.rejects(
    at(1_700_000_030 + 31).resolve(context),
    unauthenticated,
  );
});

test("without user settings assertions are rejected and service calls still work (AC-529)", async () => {
  const resolver = await createResolver(serviceEnvironment, () => {});

  await assert.rejects(
    resolver.resolve(call(vectors.delegate_token, valid)),
    unauthenticated,
  );
  assert.deepEqual(await resolver.resolve(call(vectors.delegate_token)), {
    type: "service",
    id: "bff",
  });
});

test("loads the user assertion settings", () => {
  const config = loadIdentityConfig({
    ...userEnvironment,
    IDENTITY__USER_ISSUER: " nomnom-bff ",
    IDENTITY__USER_DELEGATES: " bff , support-console ",
  });

  assert.ok(config.enabled);
  assert.ok(config.user.enabled);
  assert.equal(config.user.issuer, "nomnom-bff");
  assert.equal(config.user.jwksFile, BFF_JWKS_FILE);
  assert.deepEqual([...config.user.delegates], ["bff", "support-console"]);
});

test("user assertions are disabled without any user setting (FR-473)", () => {
  const config = loadIdentityConfig(serviceEnvironment);
  assert.ok(config.enabled);
  assert.deepEqual(config.user, { enabled: false });
});

test("partial user assertion settings are a startup error (FR-473)", () => {
  for (const environment of [
    { ...userEnvironment, IDENTITY__USER_ISSUER: "" },
    { ...userEnvironment, IDENTITY__USER_JWKS_FILE: " " },
    { ...userEnvironment, IDENTITY__USER_DELEGATES: "" },
  ]) {
    assert.throws(
      () => loadIdentityConfig(environment),
      /must be set together/,
    );
  }
});

test("rejects a malformed delegate list", () => {
  for (const delegates of ["bff,", ",bff", "bff,,orders", "b f f", "-bff"]) {
    assert.throws(
      () =>
        loadIdentityConfig({
          ...userEnvironment,
          IDENTITY__USER_DELEGATES: delegates,
        }),
      /IDENTITY__USER_DELEGATES/,
      delegates,
    );
  }
});

test("an unreadable or invalid BFF JWKS is a startup error (FR-473)", async () => {
  const directory = await mkdtemp(join(tmpdir(), "restaurants-user-identity-"));
  const write = async (name: string, content: unknown) => {
    const path = join(directory, name);
    await writeFile(
      path,
      typeof content === "string" ? content : JSON.stringify(content),
    );
    return path;
  };
  const bffKey = bffJwks.keys[0] ?? {};
  const files = [
    join(directory, "missing.json"),
    await write("not-json.json", "{"),
    await write("no-keys.json", { keys: [] }),
    await write("no-kid.json", { keys: [{ ...bffKey, kid: undefined }] }),
    // RSA-ключ кластера не годится для ES256 assertions.
    await write("rsa.json", clusterJwks),
    await write("p384.json", {
      keys: [{ ...bffKey, alg: undefined, crv: "P-384" }],
    }),
    await write("hmac.json", {
      keys: [{ kty: "oct", kid: "shared", alg: "HS256", k: "c2VjcmV0" }],
    }),
    await write("broken-key.json", {
      keys: [{ ...bffKey, x: "AA", y: "AA" }],
    }),
  ];

  for (const file of files) {
    await assert.rejects(
      createResolver({ ...userEnvironment, IDENTITY__USER_JWKS_FILE: file }),
      /JWKS/,
      file,
    );
  }
});

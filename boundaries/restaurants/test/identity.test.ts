import assert from "node:assert/strict";
import { mkdtemp, readFile, writeFile } from "node:fs/promises";
import { createServer } from "node:http2";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import {
  Code,
  ConnectError,
  createClient,
  type HandlerContext,
} from "@connectrpc/connect";
import {
  connectNodeAdapter,
  createGrpcTransport,
} from "@connectrpc/connect-node";
import type { JSONWebKeySet } from "jose";

import { RestaurantsService } from "../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import { createRestaurantsService } from "../infrastructure/grpc/service.ts";
import {
  createSubjectResolver,
  ServiceTokenSubjectResolver,
  type SubjectResolver,
} from "../infrastructure/grpc/subject.ts";
import { GrpcWorker } from "../infrastructure/grpc/worker.ts";
import type { Subject } from "../pkg/application/subject.ts";
import { loadIdentityConfig } from "../pkg/config/config.ts";
import { GetRestaurantHandler } from "../slices/restaurant/applications/get-restaurant/get-restaurant.ts";
import { ListOwnedRestaurantsHandler } from "../slices/restaurant/applications/list-owned-restaurants/list-owned-restaurants.ts";
import { ListRestaurantsHandler } from "../slices/restaurant/applications/list-restaurants/list-restaurants.ts";
import { createKitchenWorld } from "./support/kitchen.ts";

/** Общие тестовые векторы ADR 27: `<repo>/tests/identity/fixtures`. */
const FIXTURES = new URL("../../../tests/identity/fixtures/", import.meta.url);
const JWKS_FILE = new URL("jwks.json", FIXTURES).pathname;
const BFF_JWKS_FILE = new URL("bff-jwks.json", FIXTURES).pathname;

type Vectors = Readonly<{
  issuer: string;
  audience: string;
  service_accounts: string;
  expected_service: string;
  accepted: readonly string[];
  rejected: readonly string[];
  tokens: Readonly<Record<string, string>>;
}>;

const vectors = JSON.parse(
  await readFile(new URL("vectors.json", FIXTURES), "utf8"),
) as Vectors;
const jwks = JSON.parse(await readFile(JWKS_FILE, "utf8")) as JSONWebKeySet;
const bffJwks = JSON.parse(
  await readFile(BFF_JWKS_FILE, "utf8"),
) as JSONWebKeySet;

/** Конфигурация из векторов; audience векторов — `nomnom.payments`. */
const identityEnvironment = {
  IDENTITY__ISSUER: vectors.issuer,
  IDENTITY__AUDIENCE: vectors.audience,
  IDENTITY__JWKS_FILE: JWKS_FILE,
  IDENTITY__SERVICE_ACCOUNTS: vectors.service_accounts,
  IDENTITY__USER_ISSUER: "nomnom-bff",
  IDENTITY__USER_JWKS_FILE: BFF_JWKS_FILE,
  IDENTITY__USER_DELEGATES: "bff",
};

const contextWith = (headers: Record<string, string> = {}): HandlerContext =>
  ({ requestHeader: new Headers(headers) }) as unknown as HandlerContext;

const bearer = (token: string) =>
  contextWith({ authorization: `Bearer ${token}` });

const silent = () => {};

const createResolver = async (
  log: (message: string) => void = silent,
): Promise<SubjectResolver> =>
  createSubjectResolver(loadIdentityConfig(identityEnvironment), log);

const unauthenticated = (error: unknown) =>
  error instanceof ConnectError &&
  error.code === Code.Unauthenticated &&
  error.rawMessage === "verified caller identity is required";

test("accepted vectors resolve to the orders-worker service subject (AC-459, AC-461)", async () => {
  const resolver = await createResolver();
  assert.ok(resolver instanceof ServiceTokenSubjectResolver);
  assert.deepEqual(vectors.accepted, ["valid", "valid_string_aud"]);

  for (const name of vectors.accepted) {
    const token = vectors.tokens[name];
    assert.ok(token, name);
    assert.deepEqual(
      await resolver.resolve(bearer(token)),
      { type: "service", id: vectors.expected_service },
      name,
    );
  }
  assert.equal(vectors.expected_service, "orders-worker");
});

test("rejected vectors are UNAUTHENTICATED with a generic message (AC-460, AC-461)", async () => {
  const reasons: string[] = [];
  const resolver = await createResolver((message) => reasons.push(message));
  assert.ok(vectors.rejected.length > 0);

  for (const name of vectors.rejected) {
    const token = vectors.tokens[name];
    assert.ok(token, name);
    await assert.rejects(
      resolver.resolve(bearer(token)),
      unauthenticated,
      name,
    );
  }
  assert.equal(reasons.length, vectors.rejected.length);
});

test("missing and non-Bearer authorization metadata are rejected", async () => {
  const resolver = await createResolver();
  const token = vectors.tokens.valid ?? "";

  for (const context of [
    contextWith(),
    contextWith({ authorization: "" }),
    contextWith({ authorization: token }),
    contextWith({ authorization: `Basic ${token}` }),
    contextWith({ authorization: "Bearer" }),
    contextWith({ "x-service-token": token }),
  ]) {
    await assert.rejects(resolver.resolve(context), unauthenticated);
  }
});

test("expiry and not-before honour the 30s clock tolerance", async () => {
  const validFrom = 1_700_000_000_000;
  const validUntil = 4_102_444_800_000;
  const at = (milliseconds: number) =>
    new ServiceTokenSubjectResolver({
      issuer: vectors.issuer,
      audience: vectors.audience,
      jwks,
      serviceAccounts: new Map([
        ["system:serviceaccount:nomnom:orders-worker", "orders-worker"],
      ]),
      log: silent,
      now: () => new Date(milliseconds),
      userAssertions: {
        issuer: "nomnom-bff",
        jwks: bffJwks,
        delegates: new Set(["bff"]),
      },
    });
  const valid = bearer(vectors.tokens.valid ?? "");

  await at(validFrom - 29_000).resolve(valid);
  await assert.rejects(at(validFrom - 31_000).resolve(valid), unauthenticated);
  await at(validUntil + 29_000).resolve(valid);
  await assert.rejects(at(validUntil + 31_000).resolve(valid), unauthenticated);
});

test("an allowlisted token for another audience is rejected (AC-460)", async () => {
  const resolver = await createSubjectResolver(
    loadIdentityConfig({
      ...identityEnvironment,
      IDENTITY__AUDIENCE: "nomnom.restaurants",
    }),
    silent,
  );

  await assert.rejects(
    resolver.resolve(bearer(vectors.tokens.valid ?? "")),
    unauthenticated,
  );
});

test("requires issuer, audience and JWKS (AC-462)", async () => {
  for (const missing of [
    "IDENTITY__ISSUER",
    "IDENTITY__AUDIENCE",
    "IDENTITY__JWKS_FILE",
  ]) {
    const environment: Record<string, string> = { ...identityEnvironment };
    delete environment[missing];
    if (missing === "IDENTITY__ISSUER") {
      delete environment.IDENTITY__SERVICE_ACCOUNTS;
    }

    await assert.rejects(
      createSubjectResolver(loadIdentityConfig(environment), silent),
      /are required/,
      missing,
    );
  }
});

test("parses the ServiceAccount allowlist", () => {
  const config = loadIdentityConfig({
    ...identityEnvironment,
    IDENTITY__SERVICE_ACCOUNTS:
      " nomnom/orders-worker=orders-worker , nomnom/orders-api=orders ",
  });

  assert.ok(config.enabled);
  assert.deepEqual(
    [...config.serviceAccounts],
    [
      ["system:serviceaccount:nomnom:orders-worker", "orders-worker"],
      ["system:serviceaccount:nomnom:orders-api", "orders"],
    ],
  );
});

test("rejects an empty allowlist when the issuer is set", () => {
  assert.throws(
    () =>
      loadIdentityConfig({
        ...identityEnvironment,
        IDENTITY__SERVICE_ACCOUNTS: "",
      }),
    /IDENTITY__SERVICE_ACCOUNTS/,
  );
  assert.throws(
    () => loadIdentityConfig({ IDENTITY__ISSUER: vectors.issuer }),
    /IDENTITY__SERVICE_ACCOUNTS/,
  );
});

test("rejects a malformed allowlist", () => {
  for (const serviceAccounts of [
    "orders-worker",
    "nomnom/orders-worker",
    "nomnom/orders-worker=",
    "nomnom=orders-worker",
    "/orders-worker=orders-worker",
    "nomnom/orders/worker=orders-worker",
    "nomnom/orders-worker=orders=worker",
    "nomnom/orders-worker=orders-worker,",
    "Nomnom/orders-worker=orders-worker",
    "nomnom/orders-worker=a,nomnom/orders-worker=b",
  ]) {
    assert.throws(
      () =>
        loadIdentityConfig({
          ...identityEnvironment,
          IDENTITY__SERVICE_ACCOUNTS: serviceAccounts,
        }),
      /IDENTITY__SERVICE_ACCOUNTS/,
      serviceAccounts,
    );
  }
});

test("an unreadable or invalid JWKS is a startup error", async () => {
  const directory = await mkdtemp(join(tmpdir(), "restaurants-identity-"));
  const write = async (name: string, content: string) => {
    const path = join(directory, name);
    await writeFile(path, content);
    return path;
  };
  const files = [
    join(directory, "missing.json"),
    await write("not-json.json", "{"),
    await write("no-keys.json", JSON.stringify({ keys: [] })),
    await write("not-jwks.json", JSON.stringify({ kty: "RSA" })),
    await write(
      "no-kid.json",
      JSON.stringify({ keys: [{ ...jwks.keys[0], kid: undefined }] }),
    ),
    await write(
      "hmac.json",
      JSON.stringify({
        keys: [{ kty: "oct", kid: "shared", alg: "HS256", k: "c2VjcmV0" }],
      }),
    ),
    await write(
      "encryption-key.json",
      JSON.stringify({
        keys: [
          jwks.keys[0],
          { ...jwks.keys[0], kid: "encryption", use: "enc" },
        ],
      }),
    ),
    await write(
      "duplicate-kid.json",
      JSON.stringify({ keys: [jwks.keys[0], jwks.keys[0]] }),
    ),
    await write(
      "hmac-next-to-rsa.json",
      JSON.stringify({
        keys: [
          jwks.keys[0],
          { kty: "oct", kid: "shared", alg: "HS256", k: "c2VjcmV0" },
        ],
      }),
    ),
    await write(
      "broken-key.json",
      JSON.stringify({
        keys: [
          {
            kty: "EC",
            kid: "broken",
            alg: "ES256",
            crv: "P-256",
            x: "AA",
            y: "AA",
          },
        ],
      }),
    ),
  ];

  for (const file of files) {
    await assert.rejects(
      createSubjectResolver(
        loadIdentityConfig({
          ...identityEnvironment,
          IDENTITY__JWKS_FILE: file,
        }),
        silent,
      ),
      /JWKS/,
      file,
    );
  }
});

test("a gRPC call with a valid token reaches the handler as the service subject", async () => {
  const world = await createKitchenWorld();
  const { restaurantId, phoId } = await world.givenKitchen();
  const resolver = await createResolver();
  const resolved: Subject[] = [];
  const service = createRestaurantsService({
    getRestaurant: new GetRestaurantHandler({ reader: world.catalog }),
    getMenu: world.getMenu,
    listRestaurants: new ListRestaurantsHandler({ reader: world.catalog }),
    listOwnedRestaurants: new ListOwnedRestaurantsHandler({
      reader: world.catalog,
    }),
    receiveOrderRequest: world.receive,
    cancelRestaurantOrder: world.cancel,
    acceptRestaurantOrder: world.accept,
    rejectRestaurantOrder: world.reject,
    startPreparation: world.startPreparation,
    markRestaurantOrderAssembled: world.markAssembled,
    markRestaurantOrderReady: world.markReady,
    reportPreparationDelay: world.reportDelay,
    reportPreparationFailure: world.reportFailure,
    getKitchenQueue: world.getKitchenQueue,
    listIncomingOrders: { execute: async () => assert.fail("unexpected call") },
    listActiveRestaurantOrders: {
      execute: async () => assert.fail("unexpected call"),
    },
    registerRestaurant: world.register,
    assignStaff: world.assignStaff,
    revokeStaff: world.revokeStaff,
    openRestaurant: world.open,
    closeRestaurant: world.close,
    createMenu: world.createMenu,
    addMenuItem: world.addMenuItem,
    updateMenuItem: world.updateMenuItem,
    setMenuItemAvailability: world.setAvailability,
    getSimulator: { execute: () => Promise.reject(new Error("unused")) },
    setSimulatorEnabled: { execute: () => Promise.reject(new Error("unused")) },
    subjects: {
      resolve: async (context) => {
        const subject = await resolver.resolve(context);
        resolved.push(subject);
        return subject;
      },
    },
  });
  const server = createServer(
    connectNodeAdapter({
      routes: (router) => router.service(RestaurantsService, service),
    }),
  );
  const worker = new GrpcWorker(server, 0);
  await worker.start();
  const running = worker.run();

  try {
    const client = createClient(
      RestaurantsService,
      createGrpcTransport({ baseUrl: `http://127.0.0.1:${worker.port}` }),
    );
    const request = {
      restaurantId,
      orderId: "order-42",
      items: [{ menuItemId: phoId, quantity: 1 }],
    };

    await assert.rejects(client.receiveOrderRequest(request), unauthenticated);
    const received = await client.receiveOrderRequest(request, {
      headers: { authorization: `Bearer ${vectors.tokens.valid}` },
    });

    assert.equal(received.created, true);
    assert.deepEqual(resolved, [{ type: "service", id: "orders-worker" }]);
  } finally {
    await worker.shutdown(AbortSignal.timeout(1_000));
    await running;
  }
});

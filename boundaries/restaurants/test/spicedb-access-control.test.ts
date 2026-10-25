import assert from "node:assert/strict";
import test from "node:test";

import { v1 } from "@authzed/authzed-node";

import {
  type PermissionsClient,
  SpiceDbAccessControl,
} from "../infrastructure/spicedb/access-control.ts";
import { AuthorizationUnavailableError } from "../pkg/application/errors.ts";
import type { Subject } from "../pkg/application/subject.ts";
import { loadConfig } from "../pkg/config/config.ts";

const { HAS_PERMISSION, NO_PERMISSION, CONDITIONAL_PERMISSION } =
  v1.CheckPermissionResponse_Permissionship;

const user: Subject = { type: "user", id: "u-1" };
const worker: Subject = { type: "service", id: "orders-worker" };

/** Records requests and answers with a fixed permissionship or behaviour. */
class FakeClient implements PermissionsClient {
  readonly requests: v1.CheckPermissionRequest[] = [];
  readonly #answer: () => Promise<v1.CheckPermissionResponse>;

  constructor(answer: () => Promise<v1.CheckPermissionResponse>) {
    this.#answer = answer;
  }

  static answering(
    permissionship: v1.CheckPermissionResponse_Permissionship,
  ): FakeClient {
    return new FakeClient(async () =>
      v1.CheckPermissionResponse.create({ permissionship }),
    );
  }

  checkPermission(
    request: v1.CheckPermissionRequest,
  ): Promise<v1.CheckPermissionResponse> {
    this.requests.push(request);
    return this.#answer();
  }
}

const describe = (request: v1.CheckPermissionRequest): string =>
  `${request.resource?.objectType}:${request.resource?.objectId}#${request.permission}@${request.subject?.object?.objectType}:${request.subject?.object?.objectId}`;

const mapping: ReadonlyArray<{
  name: string;
  call: (access: SpiceDbAccessControl) => Promise<boolean>;
  expected: string;
}> = [
  {
    name: "canRegisterRestaurant",
    call: (access) => access.canRegisterRestaurant(user),
    expected: "platform:nomnom#register_restaurant@user:u-1",
  },
  {
    name: "canManageRestaurant",
    call: (access) => access.canManageRestaurant(user, "r-1"),
    expected: "restaurant:r-1#manage@user:u-1",
  },
  {
    name: "canManageMenu",
    call: (access) => access.canManageMenu(user, "r-1"),
    expected: "restaurant:r-1#manage_menu@user:u-1",
  },
  {
    name: "canOperateKitchen",
    call: (access) => access.canOperateKitchen(user, "r-1"),
    expected: "restaurant:r-1#operate_kitchen@user:u-1",
  },
  {
    name: "canCoordinateFulfillment",
    call: (access) => access.canCoordinateFulfillment(worker, "r-1"),
    expected: "platform:nomnom#coordinate_fulfillment@service:orders-worker",
  },
  {
    name: "canViewRestaurantOrders",
    call: (access) => access.canViewRestaurantOrders(user, "r-1"),
    expected: "restaurant:r-1#view_orders@user:u-1",
  },
];

for (const { name, call, expected } of mapping) {
  test(`${name} checks ${expected} with minimize_latency`, async () => {
    const client = FakeClient.answering(HAS_PERMISSION);
    const access = new SpiceDbAccessControl({ client, timeoutMs: 1000 });

    assert.equal(await call(access), true);
    assert.equal(client.requests.length, 1);
    const [request] = client.requests;
    assert.ok(request !== undefined);
    assert.equal(describe(request), expected);
    assert.equal(request.subject?.optionalRelation, "");
    assert.deepEqual(request.consistency?.requirement, {
      oneofKind: "minimizeLatency",
      minimizeLatency: true,
    });
  });
}

for (const permissionship of [
  NO_PERMISSION,
  CONDITIONAL_PERMISSION,
  v1.CheckPermissionResponse_Permissionship.UNSPECIFIED,
]) {
  test(`permissionship ${v1.CheckPermissionResponse_Permissionship[permissionship]} denies`, async () => {
    const access = new SpiceDbAccessControl({
      client: FakeClient.answering(permissionship),
      timeoutMs: 1000,
    });

    assert.equal(await access.canManageRestaurant(user, "r-1"), false);
  });
}

test("client error fails closed with AuthorizationUnavailableError", async () => {
  const cause = new Error("14 UNAVAILABLE: connection refused");
  const access = new SpiceDbAccessControl({
    client: new FakeClient(() => Promise.reject(cause)),
    timeoutMs: 1000,
  });

  await assert.rejects(
    access.canCoordinateFulfillment(worker, "r-1"),
    (error) => {
      assert.ok(error instanceof AuthorizationUnavailableError);
      assert.equal(error.code, "authorization_unavailable");
      assert.equal(error.cause, cause);
      return true;
    },
  );
});

test("a check exceeding the timeout fails closed", async () => {
  const access = new SpiceDbAccessControl({
    client: new FakeClient(() => new Promise(() => {})),
    timeoutMs: 20,
  });

  await assert.rejects(
    access.canOperateKitchen(user, "r-1"),
    AuthorizationUnavailableError,
  );
});

test("SpiceDB is disabled without an endpoint", () => {
  assert.deepEqual(loadConfig({}).spicedb, { enabled: false });
  assert.deepEqual(loadConfig({ SPICEDB__ENDPOINT: " " }).spicedb, {
    enabled: false,
  });
});

test("reads SpiceDB settings with a 2s default timeout", () => {
  assert.deepEqual(
    loadConfig({
      SPICEDB__ENDPOINT: "spicedb.nomnom-authz:50051",
      SPICEDB__PRESHARED_KEY: "secret",
    }).spicedb,
    {
      enabled: true,
      endpoint: "spicedb.nomnom-authz:50051",
      presharedKey: "secret",
      insecure: false,
      timeoutMs: 2000,
    },
  );
  const local = loadConfig({
    SPICEDB__ENDPOINT: "localhost:50051",
    SPICEDB__PRESHARED_KEY: "devkey",
    SPICEDB__INSECURE: "true",
    SPICEDB__TIMEOUT: "500ms",
  }).spicedb;
  assert.ok(local.enabled);
  assert.equal(local.insecure, true);
  assert.equal(local.timeoutMs, 500);
});

test("rejects an endpoint without a preshared key", () => {
  assert.throws(
    () => loadConfig({ SPICEDB__ENDPOINT: "localhost:50051" }),
    /SPICEDB__PRESHARED_KEY must not be empty/,
  );
});

test("rejects invalid SpiceDB insecure flag and timeout", () => {
  const base = {
    SPICEDB__ENDPOINT: "localhost:50051",
    SPICEDB__PRESHARED_KEY: "devkey",
  };
  assert.throws(() => loadConfig({ ...base, SPICEDB__INSECURE: "yes" }));
  assert.throws(() => loadConfig({ ...base, SPICEDB__TIMEOUT: "0s" }));
  assert.throws(() => loadConfig({ ...base, SPICEDB__TIMEOUT: "2" }));
});

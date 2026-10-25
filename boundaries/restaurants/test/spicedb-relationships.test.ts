import assert from "node:assert/strict";
import test from "node:test";

import { v1 } from "@authzed/authzed-node";

import { SpiceDbRelationshipWriter } from "../infrastructure/spicedb/relationships.ts";
import { AuthorizationUnavailableError } from "../pkg/application/errors.ts";
import { loadConfig } from "../pkg/config/config.ts";
import { FakeRelationshipsClient } from "./support/relationships.ts";

test("FR-443 writer maps owner and staff to restaurant relationships", async () => {
  const client = new FakeRelationshipsClient();
  const writer = new SpiceDbRelationshipWriter({ client, timeoutMs: 1000 });

  const revisions = [
    await writer.touchOwner({ restaurantId: "r-1", ownerId: "u-1" }),
    await writer.touchStaff({ restaurantId: "r-1", userId: "u-2" }),
    await writer.deleteStaff({ restaurantId: "r-1", userId: "u-2" }),
  ];

  assert.deepEqual(client.writes, [
    "touch restaurant:r-1#owner@user:u-1",
    "touch restaurant:r-1#staff@user:u-2",
    "delete restaurant:r-1#staff@user:u-2",
  ]);
  // FR-447: every write returns its ZedToken (`writtenAt.token`).
  assert.deepEqual(revisions, ["zed-1", "zed-2", "zed-3"]);
});

test("writer fails closed when SpiceDB returns no ZedToken", async () => {
  const writer = new SpiceDbRelationshipWriter({
    client: {
      writeRelationships: async () => v1.WriteRelationshipsResponse.create({}),
      readRelationships: async () => [],
    },
    timeoutMs: 1000,
  });

  await assert.rejects(
    writer.touchStaff({ restaurantId: "r-1", userId: "u-1" }),
    AuthorizationUnavailableError,
  );
});

test("writer fails closed on a SpiceDB error", async () => {
  const client = new FakeRelationshipsClient();
  client.failWrites = new Error("unavailable");
  const writer = new SpiceDbRelationshipWriter({ client, timeoutMs: 1000 });

  await assert.rejects(
    writer.touchOwner({ restaurantId: "r-1", ownerId: "u-1" }),
    AuthorizationUnavailableError,
  );
});

test("writer fails closed when SpiceDB does not answer within the timeout", async () => {
  const writer = new SpiceDbRelationshipWriter({
    client: {
      writeRelationships: () => new Promise(() => {}),
      readRelationships: async () => [],
    },
    timeoutMs: 20,
  });

  await assert.rejects(
    writer.touchStaff({ restaurantId: "r-1", userId: "u-1" }),
    AuthorizationUnavailableError,
  );
});

test("reconciler config defaults to 5m interval, 10m grace and 500 page size", () => {
  assert.deepEqual(loadConfig({}).reconciler, {
    intervalMs: 300_000,
    graceMs: 600_000,
    pageSize: 500,
  });
  assert.deepEqual(
    loadConfig({
      RECONCILER__INTERVAL: "30s",
      RECONCILER__GRACE: "1m",
      RECONCILER__PAGE_SIZE: "100",
    }).reconciler,
    { intervalMs: 30_000, graceMs: 60_000, pageSize: 100 },
  );
});

test("reconciler config rejects invalid values", () => {
  for (const environment of [
    { RECONCILER__INTERVAL: "0s" },
    { RECONCILER__INTERVAL: "5" },
    { RECONCILER__GRACE: "0m" },
    { RECONCILER__PAGE_SIZE: "0" },
    { RECONCILER__PAGE_SIZE: "1001" },
    { RECONCILER__PAGE_SIZE: "ten" },
  ]) {
    assert.throws(() => loadConfig(environment), JSON.stringify(environment));
  }
});

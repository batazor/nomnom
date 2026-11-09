import assert from "node:assert/strict";
import test from "node:test";

import { v1 } from "@authzed/authzed-node";
import { AuthorizationUnavailableError } from "../infrastructure/spicedb/errors.ts";
import { SpiceDbRelationshipWriter } from "../infrastructure/spicedb/relationships.ts";
import { loadConfig, specSchema } from "../pkg/config/config.ts";
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
  assert.deepEqual(loadConfig(specSchema.parse({})).reconciler, {
    intervalMs: 300_000,
    graceMs: 600_000,
    pageSize: 500,
  });
  assert.deepEqual(
    loadConfig(
      specSchema.parse({
        reconciler: { interval: "30s", grace: "1m", pageSize: 100 },
      }),
    ).reconciler,
    { intervalMs: 30_000, graceMs: 60_000, pageSize: 100 },
  );
});

test("reconciler config rejects invalid values", () => {
  for (const reconciler of [
    { interval: "0s" },
    { interval: "5" },
    { grace: "0m" },
    { pageSize: 0 },
    { pageSize: 1001 },
    { pageSize: "ten" },
  ]) {
    assert.throws(
      () => specSchema.parse({ reconciler }),
      JSON.stringify(reconciler),
    );
  }
});

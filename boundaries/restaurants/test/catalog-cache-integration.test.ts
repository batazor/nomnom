import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { after, before, describe, test } from "node:test";

import { Valkey } from "iovalkey";

import { connectValkey } from "../pkg/infrastructure/valkey/client.ts";
import {
  CacheMetrics,
  ReadCache,
} from "../pkg/infrastructure/valkey/read-cache.ts";
import { silentLogger } from "../pkg/telemetry/logger.ts";
import { Menu } from "../slices/menu/domains/menu/menu.ts";
import { CachedMenuReader } from "../slices/menu/infrastructure/cache/menus.ts";
import {
  createMenuCollection,
  MongoMenuReader,
  MongoMenuRepository,
} from "../slices/menu/infrastructure/mongodb/menus.ts";
import { valkeyUrl } from "./support/global-containers.ts";
import { createTestDatabase } from "./support/mongodb.ts";
import { testValkeyConfig } from "./support/valkey.ts";

type View = Readonly<{ name: string }>;

/** A source that counts its reads. */
const source = (name: string) => {
  const state = { name, reads: 0 };
  return {
    state,
    load: async (): Promise<View> => {
      state.reads += 1;
      return { name: state.name };
    },
  };
};

/** Stores are not awaited by `read`: wait until the key holds a value. */
const stored = async (client: Valkey, key: string) => {
  for (let attempt = 0; attempt < 50; attempt += 1) {
    if ((await client.get(key)) !== null) return;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  assert.fail(`${key} was not stored`);
};

const ready = (client: Valkey) =>
  new Promise<void>((resolve) => client.once("ready", resolve));

describe("catalog read cache on Valkey (ADR 40)", () => {
  let client: Valkey;
  const prefix = `restaurants:test-${randomUUID()}:`;
  const cacheOf = (ttlMs: number) =>
    new ReadCache<View>({
      client,
      name: "test",
      prefix,
      ttlMs,
      metrics: new CacheMetrics(),
    });

  before(async () => {
    client = new Valkey(valkeyUrl());
    await ready(client);
  });

  after(() => client.quit());

  test("a miss reads the source and stores with the TTL, the next read is a hit", async () => {
    const cache = cacheOf(60_000);
    const id = randomUUID();
    const { state, load } = source("v1");

    assert.deepEqual(await cache.read(id, load), { name: "v1" });
    await stored(client, prefix + id);
    assert.deepEqual(await cache.read(id, load), { name: "v1" });
    assert.equal(state.reads, 1);
    const ttl = await client.pttl(prefix + id);
    assert.ok(ttl > 0 && ttl <= 60_000, `TTL ${ttl}`);
  });

  test("a change is read from the source once the entry expires", async () => {
    const cache = cacheOf(100);
    const id = randomUUID();
    const { state, load } = source("old");
    await cache.read(id, load);
    await stored(client, prefix + id);

    state.name = "new";
    assert.deepEqual(await cache.read(id, load), { name: "old" });
    await new Promise((resolve) => setTimeout(resolve, 200));
    assert.deepEqual(await cache.read(id, load), { name: "new" });
    assert.equal(state.reads, 2);
  });

  test("a missing source value is not cached", async () => {
    const cache = cacheOf(60_000);
    const id = randomUUID();
    let reads = 0;
    const missing = async () => {
      reads += 1;
      return undefined;
    };
    assert.equal(await cache.read(id, missing), undefined);
    assert.equal(await cache.read(id, missing), undefined);
    assert.equal(reads, 2);
    assert.equal(await client.get(prefix + id), null);
  });
});

describe("catalog read cache without Valkey (ADR 40)", () => {
  // Nothing listens on port 1: the client never becomes ready.
  const valkey = connectValkey(testValkeyConfig("redis://127.0.0.1:1"), {
    connectionName: "test",
    logger: silentLogger,
  });
  const cache = new ReadCache<View>({
    client: valkey.client,
    name: "test",
    prefix: "restaurants:test:",
    ttlMs: 60_000,
    metrics: new CacheMetrics(),
  });

  after(() => valkey.close());

  test("reads bypass the cache and come from the source", async () => {
    const { state, load } = source("v1");
    assert.deepEqual(await cache.read("r-1", load), { name: "v1" });
    assert.deepEqual(await cache.read("r-1", load), { name: "v1" });
    assert.equal(state.reads, 2);
  });
});

test("the cached menu reader serves the MongoDB menu", async () => {
  const { db, unitOfWork } = await createTestDatabase();
  const collection = createMenuCollection(db, unitOfWork);
  const restaurantId = `r-${randomUUID()}`;
  await new MongoMenuRepository(collection).save(
    Menu.create({
      id: `m-${randomUUID()}`,
      restaurantId,
      createdAt: new Date(),
    }),
  );
  const client = new Valkey(valkeyUrl());
  try {
    await ready(client);
    const reader = new CachedMenuReader(new MongoMenuReader(collection), {
      client,
      ttlMs: 60_000,
      metrics: new CacheMetrics(),
    });
    assert.equal(
      (await reader.getByRestaurantId(restaurantId))?.restaurantId,
      restaurantId,
    );
    await stored(client, `restaurants:menu:${restaurantId}`);
    assert.equal(
      (await reader.getByRestaurantId(restaurantId))?.restaurantId,
      restaurantId,
    );
    assert.equal(await reader.getByRestaurantId("missing"), undefined);
  } finally {
    await client.quit();
  }
});

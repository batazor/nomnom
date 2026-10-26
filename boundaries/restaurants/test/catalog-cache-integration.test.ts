import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { after, before, describe, test } from "node:test";

import { create, toBinary } from "@bufbuild/protobuf";
import { Valkey } from "iovalkey";

import {
  MenuItemAvailabilityChangedSchema,
  RestaurantClosedSchema,
} from "../infrastructure/grpc/gen/nomnom/restaurants/v1/events_pb.js";

import { connectValkey } from "../pkg/infrastructure/valkey/client.ts";
import {
  CacheMetrics,
  ReadCache,
} from "../pkg/infrastructure/valkey/read-cache.ts";
import { CacheRefreshHandler } from "../pkg/infrastructure/valkey/refresh.ts";
import { silentLogger } from "../pkg/telemetry/logger.ts";
import type { MenuView } from "../slices/menu/applications/menu-support.ts";
import { Menu } from "../slices/menu/domains/menu/menu.ts";
import {
  CachedMenuReader,
  menuCacheRefreshes,
} from "../slices/menu/infrastructure/cache/menus.ts";
import {
  createMenuCollection,
  MongoMenuReader,
  MongoMenuRepository,
} from "../slices/menu/infrastructure/repository/menus.ts";
import type { RestaurantView } from "../slices/restaurant/applications/restaurant-view.ts";
import {
  CachedRestaurantCatalog,
  restaurantCacheRefreshes,
} from "../slices/restaurant/infrastructure/cache/restaurants.ts";
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

/** Kafka message of an Integration Event as Debezium publishes it. */
const eventMessage = (eventType: string, payload: Uint8Array) => ({
  topic: "restaurants.v1.restaurant-events",
  partition: 0,
  offset: "0",
  key: undefined,
  value: payload,
  headers: {
    event_id: randomUUID(),
    event_type: eventType,
    occurred_at: new Date().toISOString(),
    aggregate_type: "Restaurant",
    aggregate_id: "r",
    aggregate_version: "2",
  },
});

/** Restaurants stored "in MongoDB": the test changes them like a command. */
const restaurantSource = () => {
  const stored = new Map<string, RestaurantView>();
  let reads = 0;
  return {
    stored,
    reads: () => reads,
    source: {
      get: async (id: string) => {
        reads += 1;
        return stored.get(id);
      },
      list: async (criteria: { includeClosed: boolean }) => {
        reads += 1;
        return [...stored.values()].filter(
          (view) => criteria.includeClosed || view.isOpen,
        );
      },
    },
  };
};

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

describe("refresh by Integration Events (ADR 40)", () => {
  let client: Valkey;
  const options = () => ({
    client,
    ttlMs: 60_000,
    metrics: new CacheMetrics(),
  });

  before(async () => {
    client = new Valkey(valkeyUrl());
    await ready(client);
  });

  after(() => client.quit());

  test("RestaurantClosed rewrites the cached restaurant and drops the list pages", async () => {
    const id = `r-${randomUUID()}`;
    const world = restaurantSource();
    world.stored.set(id, {
      restaurantId: id,
      name: "Pho",
      isOpen: true,
      address: "1 St",
    });
    const catalog = new CachedRestaurantCatalog(world.source, options());
    const page = { includeClosed: false, after: undefined, limit: 1000 };

    assert.equal((await catalog.get(id))?.isOpen, true);
    await stored(client, `restaurants:restaurant:${id}`);
    assert.ok(
      (await catalog.list(page)).some((view) => view.restaurantId === id),
    );
    await new Promise((resolve) => setTimeout(resolve, 50));

    // Close Restaurant committed; Debezium delivers RestaurantClosed.
    world.stored.set(id, {
      restaurantId: id,
      name: "Pho",
      isOpen: false,
      address: "1 St",
    });
    const handler = new CacheRefreshHandler({
      refreshes: restaurantCacheRefreshes(catalog),
      retry: { initialIntervalMs: 10, maxIntervalMs: 50 },
      warn: () => {},
    });
    const closed = toBinary(
      RestaurantClosedSchema,
      create(RestaurantClosedSchema, { restaurantId: id }),
    );
    assert.equal(
      await handler.process(
        eventMessage(RestaurantClosedSchema.typeName, closed),
        new AbortController().signal,
      ),
      "processed",
    );

    const reads = world.reads();
    assert.equal(
      (await catalog.get(id))?.isOpen,
      false,
      "refreshed, not expired",
    );
    assert.equal(world.reads(), reads, "served from the cache");
    assert.equal(
      (await catalog.list(page)).some((view) => view.restaurantId === id),
      false,
      "the list page of the old generation is not read",
    );
  });

  test("a read that started before the change does not overwrite the refresh", async () => {
    const id = `r-${randomUUID()}`;
    const world = restaurantSource();
    world.stored.set(id, {
      restaurantId: id,
      name: "Pho",
      isOpen: true,
      address: "",
    });
    let staleReadDone = false;
    const catalog: CachedRestaurantCatalog = new CachedRestaurantCatalog(
      {
        ...world.source,
        // The first read is stale: it returns the open restaurant after the
        // close committed and the refresh ran. The refresh reads normally.
        get: async (restaurantId: string) => {
          const view = await world.source.get(restaurantId);
          if (!staleReadDone) {
            staleReadDone = true;
            world.stored.set(id, {
              restaurantId: id,
              name: "Pho",
              isOpen: false,
              address: "",
            });
            await catalog.refresh(id);
          }
          return view;
        },
      },
      options(),
    );

    assert.equal(
      (await catalog.get(id))?.isOpen,
      true,
      "the stale read itself",
    );
    await new Promise((resolve) => setTimeout(resolve, 50));
    const cached = JSON.parse(
      (await client.get(`restaurants:restaurant:${id}`)) ?? "null",
    ) as RestaurantView;
    assert.equal(cached.isOpen, false, "SET NX of the stale read lost");
  });

  test("MenuItemAvailabilityChanged rewrites the cached menu", async () => {
    const restaurantId = `r-${randomUUID()}`;
    let available = true;
    const menu = (): MenuView => ({
      menuId: "m",
      restaurantId,
      items: [
        {
          menuItemId: "pho",
          name: "Pho",
          description: undefined,
          price: { amount: "9.50", currency: "EUR" },
          isAvailable: available,
        },
      ],
    });
    const menus = new CachedMenuReader(
      { getByRestaurantId: async () => menu() },
      options(),
    );
    await menus.getByRestaurantId(restaurantId);
    await stored(client, `restaurants:menu:${restaurantId}`);

    available = false;
    const handler = new CacheRefreshHandler({
      refreshes: menuCacheRefreshes(menus),
      retry: { initialIntervalMs: 10, maxIntervalMs: 50 },
      warn: () => {},
    });
    const changed = toBinary(
      MenuItemAvailabilityChangedSchema,
      create(MenuItemAvailabilityChangedSchema, {
        menuId: "m",
        restaurantId,
        menuItemId: "pho",
        available: false,
      }),
    );
    await handler.process(
      eventMessage(MenuItemAvailabilityChangedSchema.typeName, changed),
      new AbortController().signal,
    );
    assert.equal(
      (await menus.getByRestaurantId(restaurantId))?.items[0]?.isAvailable,
      false,
    );
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

  test("a refresh keeps retrying until the consumer stops", async () => {
    const world = restaurantSource();
    const handler = new CacheRefreshHandler({
      refreshes: restaurantCacheRefreshes(
        new CachedRestaurantCatalog(world.source, {
          client: valkey.client,
          ttlMs: 60_000,
          metrics: new CacheMetrics(),
        }),
      ),
      retry: { initialIntervalMs: 10, maxIntervalMs: 20 },
      warn: () => {},
    });
    const abort = new AbortController();
    const processing = handler.process(
      eventMessage(
        RestaurantClosedSchema.typeName,
        toBinary(
          RestaurantClosedSchema,
          create(RestaurantClosedSchema, { restaurantId: "r-1" }),
        ),
      ),
      abort.signal,
    );
    setTimeout(() => abort.abort(), 100);
    // Rejected, so the offset is not committed and the event is redelivered.
    await assert.rejects(processing);
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

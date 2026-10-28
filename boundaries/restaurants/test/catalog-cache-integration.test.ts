import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { after, before, describe, test } from "node:test";

import { create, toBinary } from "@bufbuild/protobuf";
import { Valkey } from "iovalkey";

import {
  MenuItemAvailabilityChangedSchema,
  RestaurantClosedSchema,
  RestaurantWaitLevelChangedSchema,
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
  DatabaseMenuReader,
  DatabaseMenuRepository,
} from "../slices/menu/infrastructure/repository/menus.ts";
import type {
  CatalogRestaurantView,
  RestaurantView,
} from "../slices/restaurant/applications/restaurant-view.ts";
import {
  CachedRestaurantCatalog,
  restaurantCacheRefreshes,
} from "../slices/restaurant/infrastructure/cache/restaurants.ts";
import { createTestDatabase } from "./support/database.ts";
import { valkeyUrl } from "./support/global-containers.ts";
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
const NORMAL_WAIT = {
  elevated: false,
  expectedWaitMinutes: 20,
  baselinePreparationMs: 20 * 60_000,
};

const restaurantSource = () => {
  const stored = new Map<string, RestaurantView>();
  const waits = new Map<string, CatalogRestaurantView["waitTime"]>();
  const catalogView = (view: RestaurantView): CatalogRestaurantView => ({
    ...view,
    waitTime: waits.get(view.restaurantId) ?? NORMAL_WAIT,
  });
  let reads = 0;
  return {
    stored,
    waits,
    reads: () => reads,
    source: {
      get: async (id: string) => {
        reads += 1;
        const view = stored.get(id);
        return view === undefined ? undefined : catalogView(view);
      },
      getMany: async (ids: readonly string[]) => {
        reads += 1;
        return ids.flatMap((id) => {
          const view = stored.get(id);
          return view === undefined ? [] : [catalogView(view)];
        });
      },
      list: async (criteria: { marketId: string; includeClosed: boolean }) => {
        reads += 1;
        return [...stored.values()]
          .filter(
            (view) =>
              view.marketId === criteria.marketId &&
              (criteria.includeClosed || view.isOpen),
          )
          .map(catalogView);
      },
    },
  };
};

describe("catalog read cache on Valkey (ADR 43)", () => {
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

  test("readMany takes hits from one MGET and loads only the misses in one call", async () => {
    const cache = cacheOf(60_000);
    const [cachedId, missingId, unknownId] = [
      randomUUID(),
      randomUUID(),
      randomUUID(),
    ];
    const { load } = source("cached");
    await cache.read(cachedId, load);
    await stored(client, prefix + cachedId);
    const batches: (readonly string[])[] = [];
    const loadMany = async (missing: readonly string[]) => {
      batches.push(missing);
      return new Map(
        missing
          .filter((id) => id !== unknownId)
          .map((id) => [id, { name: "loaded" }] as const),
      );
    };

    const result = await cache.readMany(
      [cachedId, missingId, unknownId],
      loadMany,
    );

    assert.deepEqual(
      result,
      new Map([
        [cachedId, { name: "cached" }],
        [missingId, { name: "loaded" }],
      ]),
    );
    assert.deepEqual(batches, [[missingId, unknownId]]);
    await stored(client, prefix + missingId);
    assert.equal(await client.get(prefix + unknownId), null);
  });

  test("readMany of keys all cached does not call the source", async () => {
    const cache = cacheOf(60_000);
    const ids = [randomUUID(), randomUUID()];
    for (const id of ids) {
      await cache.read(id, source(id).load);
      await stored(client, prefix + id);
    }

    const result = await cache.readMany(ids, () =>
      assert.fail("the source must not be read"),
    );

    assert.deepEqual([...result.keys()].sort(), [...ids].sort());
  });
});

describe("Batch Get Restaurants through the cache (ADR 7, ADR 43)", () => {
  let client: Valkey;

  before(async () => {
    client = new Valkey(valkeyUrl());
    await ready(client);
  });

  after(() => client.quit());

  test("a page of restaurants is one source read, the repeat is served by Valkey", async () => {
    const world = restaurantSource();
    const ids = [`r-${randomUUID()}`, `r-${randomUUID()}`];
    for (const id of ids) {
      world.stored.set(id, {
        restaurantId: id,
        name: id,
        marketId: "berlin",
        timeZone: "Europe/Berlin",
        openingHours: [],
        isOpen: true,
      });
    }
    const catalog = new CachedRestaurantCatalog(world.source, {
      client,
      ttlMs: 60_000,
      metrics: new CacheMetrics(),
    });

    const first = await catalog.getMany([...ids, "r-unknown"]);
    assert.deepEqual(first.map((view) => view.restaurantId).sort(), ids.sort());
    assert.equal(world.reads(), 1);
    for (const id of ids) await stored(client, `restaurants:restaurant:${id}`);

    const second = await catalog.getMany(ids);
    assert.equal(second.length, 2);
    assert.equal(world.reads(), 1);
  });
});

describe("refresh by Integration Events (ADR 43)", () => {
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
      marketId: "berlin",
      timeZone: "Europe/Berlin",
      openingHours: [],
      isOpen: true,
    });
    const catalog = new CachedRestaurantCatalog(world.source, options());
    const page = {
      marketId: "berlin",
      includeClosed: false,
      after: undefined,
      limit: 1000,
    };

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
      marketId: "berlin",
      timeZone: "Europe/Berlin",
      openingHours: [],
      isOpen: false,
    });
    const handler = new CacheRefreshHandler({
      refreshes: restaurantCacheRefreshes(catalog),
      retry: { initialIntervalMs: 10, maxIntervalMs: 50 },
      warn: () => {},
    });
    const closed = toBinary(
      RestaurantClosedSchema,
      create(RestaurantClosedSchema, { restaurantId: id, marketId: "berlin" }),
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

  test("FR-646 RestaurantWaitLevelChanged rewrites the cached wait and drops the list pages", async () => {
    const id = `r-${randomUUID()}`;
    const world = restaurantSource();
    world.stored.set(id, {
      restaurantId: id,
      name: "Pho",
      marketId: "berlin",
      timeZone: "Europe/Berlin",
      openingHours: [],
      isOpen: true,
    });
    const catalog = new CachedRestaurantCatalog(world.source, options());
    const page = {
      marketId: "berlin",
      includeClosed: false,
      after: undefined,
      limit: 1000,
    };
    assert.equal((await catalog.get(id))?.waitTime.elevated, false);
    await stored(client, `restaurants:restaurant:${id}`);
    await catalog.list(page);
    await new Promise((resolve) => setTimeout(resolve, 50));

    world.waits.set(id, {
      elevated: true,
      expectedWaitMinutes: 42,
      baselinePreparationMs: 20 * 60_000,
    });
    const handler = new CacheRefreshHandler({
      refreshes: restaurantCacheRefreshes(catalog),
      retry: { initialIntervalMs: 10, maxIntervalMs: 50 },
      warn: () => {},
    });
    const changed = toBinary(
      RestaurantWaitLevelChangedSchema,
      create(RestaurantWaitLevelChangedSchema, {
        restaurantId: id,
        marketId: "berlin",
        elevated: true,
      }),
    );
    assert.equal(
      await handler.process(
        eventMessage(RestaurantWaitLevelChangedSchema.typeName, changed),
        new AbortController().signal,
      ),
      "processed",
    );

    const reads = world.reads();
    assert.deepEqual((await catalog.get(id))?.waitTime, {
      elevated: true,
      expectedWaitMinutes: 42,
      baselinePreparationMs: 20 * 60_000,
    });
    assert.equal(world.reads(), reads, "refreshed, served from the cache");
    assert.equal(
      (await catalog.list(page)).find((view) => view.restaurantId === id)
        ?.waitTime.elevated,
      true,
      "the list page of the old generation is not read",
    );
  });

  test("ADR 41 list pages are keyed per Market and refreshed only in the restaurant's Market", async () => {
    // Markets unique to this run: generations are shared through Valkey.
    const berlin = `berlin-${randomUUID().slice(0, 8)}`;
    const hamburg = `hamburg-${randomUUID().slice(0, 8)}`;
    const berlinId = `r-${randomUUID()}`;
    const hamburgId = `r-${randomUUID()}`;
    const world = restaurantSource();
    world.stored.set(berlinId, {
      restaurantId: berlinId,
      name: "Berlin Pho",
      marketId: berlin,
      timeZone: "Europe/Berlin",
      openingHours: [],
      isOpen: true,
    });
    world.stored.set(hamburgId, {
      restaurantId: hamburgId,
      name: "Hamburg Pho",
      marketId: hamburg,
      timeZone: "Europe/Berlin",
      openingHours: [],
      isOpen: true,
    });
    const catalog = new CachedRestaurantCatalog(world.source, options());
    const pageOf = (marketId: string) => ({
      marketId,
      includeClosed: false,
      after: undefined,
      limit: 1000,
    });
    const ids = async (marketId: string) =>
      (await catalog.list(pageOf(marketId))).map((view) => view.restaurantId);

    assert.deepEqual(await ids(berlin), [berlinId]);
    assert.deepEqual(await ids(hamburg), [hamburgId]);
    await new Promise((resolve) => setTimeout(resolve, 50));
    assert.deepEqual(await ids(berlin), [berlinId], "cached, not mixed");
    assert.deepEqual(await ids(hamburg), [hamburgId], "cached, not mixed");

    // Close Restaurant of the Berlin restaurant committed.
    world.stored.set(berlinId, {
      restaurantId: berlinId,
      name: "Berlin Pho",
      marketId: berlin,
      timeZone: "Europe/Berlin",
      openingHours: [],
      isOpen: false,
    });
    await catalog.refresh(berlinId);
    const reads = world.reads();

    assert.deepEqual(await ids(hamburg), [hamburgId]);
    assert.equal(world.reads(), reads, "Hamburg pages are kept");
    assert.deepEqual(await ids(berlin), []);
    assert.equal(world.reads(), reads + 1, "Berlin pages are dropped");
  });

  test("a read that started before the change does not overwrite the refresh", async () => {
    const id = `r-${randomUUID()}`;
    const world = restaurantSource();
    world.stored.set(id, {
      restaurantId: id,
      name: "Pho",
      marketId: "berlin",
      timeZone: "Europe/Berlin",
      openingHours: [],
      isOpen: true,
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
              marketId: "berlin",
              timeZone: "Europe/Berlin",
              openingHours: [],
              isOpen: false,
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
      {
        getByRestaurantId: async () => menu(),
        getManyByRestaurantId: async () => [menu()],
      },
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

describe("catalog read cache without Valkey (ADR 43)", () => {
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

  test("readMany bypasses the cache and loads every key from the source", async () => {
    const batches: (readonly string[])[] = [];
    const result = await cache.readMany(["r-1", "r-2"], async (missing) => {
      batches.push(missing);
      return new Map(missing.map((id) => [id, { name: id }] as const));
    });
    assert.deepEqual(batches, [["r-1", "r-2"]]);
    assert.equal(result.size, 2);
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
  await new DatabaseMenuRepository(collection).save(
    Menu.create({
      id: `m-${randomUUID()}`,
      restaurantId,
      marketId: "berlin",
      createdAt: new Date(),
    }),
  );
  const client = new Valkey(valkeyUrl());
  try {
    await ready(client);
    const reader = new CachedMenuReader(new DatabaseMenuReader(collection), {
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

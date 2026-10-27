import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { after, before, describe, test } from "node:test";

import { createClient } from "@connectrpc/connect";
import { createGrpcTransport } from "@connectrpc/connect-node";
import {
  InMemorySpanExporter,
  SimpleSpanProcessor,
} from "@opentelemetry/sdk-trace";

import { RestaurantsService } from "../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import { createGrpcServer } from "../infrastructure/grpc/server.ts";
import { createRestaurantsService } from "../infrastructure/grpc/service.ts";
import { GrpcWorker } from "../infrastructure/grpc/worker.ts";
import { migrateRestaurantsDatabase } from "../infrastructure/repository/migrate.ts";
import type { Subject } from "../pkg/application/subject.ts";
import { outboxEventMapperByAggregate } from "../pkg/infrastructure/outbox/integration-event.ts";
import {
  connectDatabase,
  type DatabaseConnection,
} from "../pkg/infrastructure/repository/client.ts";
import {
  DatabaseOutbox,
  OUTBOX_COLLECTION,
  type OutboxDocument,
} from "../pkg/infrastructure/repository/outbox.ts";
import { DatabaseUnitOfWork } from "../pkg/infrastructure/repository/unit-of-work.ts";
import { currentTraceMetadata } from "../pkg/telemetry/trace-context.ts";
import { startTracing, type Tracing } from "../pkg/telemetry/tracing.ts";
import { SetMenuItemAvailabilityHandler } from "../slices/menu/applications/set-menu-item-availability/set-menu-item-availability.ts";
import { Menu } from "../slices/menu/domains/menu/menu.ts";
import { Money } from "../slices/menu/domains/menu/vo/money.ts";
import {
  MENU_AGGREGATE_TYPE,
  menuEventMapper,
} from "../slices/menu/infrastructure/integration/menu-events.ts";
import {
  createMenuCollection,
  DatabaseMenuRepository,
} from "../slices/menu/infrastructure/repository/menus.ts";
import { CloseRestaurantHandler } from "../slices/restaurant/applications/close-restaurant/close-restaurant.ts";
import { OpenRestaurantHandler } from "../slices/restaurant/applications/open-restaurant/open-restaurant.ts";
import { Restaurant } from "../slices/restaurant/domains/restaurant/restaurant.ts";
import {
  RESTAURANT_AGGREGATE_TYPE,
  RESTAURANT_EVENTS_TOPIC,
  restaurantEventMapper,
} from "../slices/restaurant/infrastructure/integration/restaurant-events.ts";
import {
  createRestaurantCollection,
  DatabaseRestaurantRepository,
} from "../slices/restaurant/infrastructure/repository/restaurants.ts";
import { databaseUri, noKitchenChanges } from "./support/database.ts";
import { TestMenuAccessControl } from "./support/menus.ts";

// Open Restaurant and Set Menu Item Availability over HTTP/2 (createGrpcServer
// as in cmd/restaurants) with the MongoDB Unit of Work and DatabaseOutbox of the
// process (Integration Event mapper, trace metadata) against a replica set:
// MongoDB is the testcontainer of the global setup (test/support/global-containers.ts).
const uri = databaseUri();

const TRACE_ID = "0af7651916cd43dd8448eb211c80319c";
const TRACEPARENT = `00-${TRACE_ID}-b7ad6b7169203331-01`;
const manager: Subject = { type: "user", id: "owner" };

describe("restaurant and menu command RPCs over MongoDB", () => {
  let database: DatabaseConnection;
  let tracing: Tracing;
  let worker: GrpcWorker;
  let running: Promise<void>;
  let client: ReturnType<typeof createClient<typeof RestaurantsService>>;
  const restaurantId = `r-${randomUUID()}`;
  const menuId = `m-${randomUUID()}`;

  before(async () => {
    tracing = startTracing(
      "restaurants",
      {},
      {
        spanProcessors: [
          new SimpleSpanProcessor({ exporter: new InMemorySpanExporter() }),
        ],
      },
    );
    database = await connectDatabase({
      enabled: true,
      uri,
      database: `restaurants_commands_${randomUUID().slice(0, 8)}`,
      timeoutMs: 5000,
    });
    await migrateRestaurantsDatabase(database.db);
    const unitOfWork = new DatabaseUnitOfWork(database.client);
    const outbox = new DatabaseOutbox(database.db, unitOfWork, {
      mapper: outboxEventMapperByAggregate({
        [RESTAURANT_AGGREGATE_TYPE]: restaurantEventMapper,
        [MENU_AGGREGATE_TYPE]: menuEventMapper,
      }),
      metadata: currentTraceMetadata,
    });
    const restaurants = new DatabaseRestaurantRepository(
      createRestaurantCollection(database.db, unitOfWork),
      noKitchenChanges,
    );
    const menus = new DatabaseMenuRepository(
      createMenuCollection(database.db, unitOfWork),
    );
    const clock = { now: () => new Date() };
    await restaurants.save(
      Restaurant.register({
        id: restaurantId,
        name: "Pho House",
        ownerId: manager.id,
        marketId: "berlin",
        registeredAt: clock.now(),
      }),
    );
    const menu = Menu.create({
      id: menuId,
      restaurantId,
      marketId: "berlin",
      createdAt: clock.now(),
    });
    menu.addItem({
      id: "pho",
      name: "Pho",
      price: Money.from({ amount: "4.50", currency: "USD" }),
      isAvailable: true,
      addedAt: clock.now(),
    });
    await menus.save(menu);

    const restaurantAccess = {
      canRegisterRestaurant: async () => false,
      canManageRestaurant: async (subject: Subject, id: string) =>
        subject.id === manager.id && id === restaurantId,
    };
    const opening = {
      accessControl: restaurantAccess,
      unitOfWork,
      restaurants,
      outbox,
      clock,
    };
    const unused = {
      execute: async (): Promise<never> => assert.fail("unexpected call"),
    };
    const service = createRestaurantsService({
      subjects: { resolve: async () => manager },
      getRestaurant: unused,
      getMenu: unused,
      batchGetRestaurants: unused,
      batchGetMenus: unused,
      listRestaurants: unused,
      listOwnedRestaurants: unused,
      receiveOrderRequest: unused,
      cancelRestaurantOrder: unused,
      acceptRestaurantOrder: unused,
      rejectRestaurantOrder: unused,
      startPreparation: unused,
      markRestaurantOrderAssembled: unused,
      markRestaurantOrderReady: unused,
      reportPreparationDelay: unused,
      reportPreparationFailure: unused,
      getKitchenQueue: unused,
      listIncomingOrders: unused,
      listActiveRestaurantOrders: unused,
      registerRestaurant: unused,
      updateRestaurant: unused,
      assignStaff: unused,
      revokeStaff: unused,
      openRestaurant: new OpenRestaurantHandler(opening),
      closeRestaurant: new CloseRestaurantHandler(opening),
      createMenu: unused,
      addMenuItem: unused,
      updateMenuItem: unused,
      setMenuItemAvailability: new SetMenuItemAvailabilityHandler({
        accessControl: new TestMenuAccessControl().grantManager(
          manager,
          restaurantId,
        ),
        unitOfWork,
        menus,
        outbox,
        clock,
      }),
      getSimulator: unused,
      setSimulatorEnabled: unused,
      hireCook: unused,
      putCookOnLine: unused,
      takeCookOffLine: unused,
      retireCook: unused,
      getCookPool: unused,
    });
    worker = new GrpcWorker(createGrpcServer(service), 0);
    await worker.start();
    running = worker.run();
    client = createClient(
      RestaurantsService,
      createGrpcTransport({ baseUrl: `http://127.0.0.1:${worker.port}` }),
    );
  });

  after(async () => {
    if (worker !== undefined) {
      await worker.shutdown(AbortSignal.timeout(2000));
      await running;
    }
    if (database !== undefined) {
      await database.db.dropDatabase();
      await database.close();
    }
    await tracing?.shutdown();
  });

  const outboxRecords = (aggregateId: string) =>
    database.db
      .collection<OutboxDocument>(OUTBOX_COLLECTION)
      .find({ aggregateId })
      .sort({ aggregateVersion: 1 })
      .toArray();

  test("AC-711 OpenRestaurant over gRPC writes RestaurantOpened to the Outbox in the caller's trace", async () => {
    const response = await client.openRestaurant(
      { restaurantId },
      { headers: { traceparent: TRACEPARENT } },
    );
    assert.equal(response.changed, true);
    assert.equal(response.restaurant?.isOpen, true);
    assert.equal(response.version, 2n);

    const records = await outboxRecords(restaurantId);
    assert.equal(records.length, 1);
    const [record] = records;
    assert.equal(record?.eventType, "nomnom.restaurants.v1.RestaurantOpened");
    assert.equal(record?.topic, RESTAURANT_EVENTS_TOPIC);
    assert.equal(record?.aggregateType, RESTAURANT_AGGREGATE_TYPE);
    assert.equal(record?.aggregateVersion, 2);
    assert.match(
      record?.traceparent ?? "",
      new RegExp(`^00-${TRACE_ID}-[0-9a-f]{16}-01$`),
    );

    // AC-209: a repeat succeeds without a new record.
    const repeated = await client.openRestaurant({ restaurantId });
    assert.equal(repeated.changed, false);
    assert.equal((await outboxRecords(restaurantId)).length, 1);
  });
});

import type { Db } from "mongodb";

import type { CollectionSpec } from "../../../../pkg/infrastructure/mongodb/migrate.ts";
import type { MongoUnitOfWork } from "../../../../pkg/infrastructure/mongodb/unit-of-work.ts";
import {
  VersionedCollection,
  type VersionedDocument,
} from "../../../../pkg/infrastructure/mongodb/versioned-collection.ts";
import type {
  KitchenQueueEntry,
  KitchenQueueReader,
} from "../../applications/get-kitchen-queue/get-kitchen-queue.ts";
import type { RestaurantOrderReader } from "../../applications/get-restaurant-order/get-restaurant-order.ts";
import type {
  ActivePreparation,
  ActivePreparationsReader,
} from "../../applications/list-active-preparations/list-active-preparations.ts";
import type {
  ReadyOrder,
  ReadyOrdersReader,
} from "../../applications/list-ready-orders/list-ready-orders.ts";
import {
  type RestaurantOrderView,
  snapshotToView,
} from "../../applications/restaurant-order-support.ts";
import {
  DuplicateOrderIdError,
  RestaurantOrder,
  type RestaurantOrderRepository,
  type RestaurantOrderSnapshot,
  RestaurantOrderState,
} from "../../domains/restaurantorder/restaurantorder.ts";

export const RESTAURANT_ORDERS_COLLECTION = "restaurant_orders";
export const RESTAURANT_ORDER_SCHEMA_VERSION = 1;

/**
 * One document per Restaurant Order with its Preparation Items and timeline
 * embedded (bounded by one order). It references Restaurant and the `orders`
 * Order only by id; `orderId` is unique (idempotent Receive).
 */
export type RestaurantOrderDocument = VersionedDocument & {
  orderId: string;
  restaurantId: string;
  items: { menuItemId: string; quantity: number; options: string[] }[];
  state: RestaurantOrderState;
  timeline: {
    acceptedAt?: Date;
    queuedAt?: Date;
    preparationStartedAt?: Date;
    expectedReadyAt?: Date;
    readyAt?: Date;
  };
  rejectionReason?: string;
  failureReason?: string;
  cancellation?: { stage: RestaurantOrderState; reason?: string };
};

const toSnapshot = (
  document: RestaurantOrderDocument,
): RestaurantOrderSnapshot => ({
  id: document._id,
  orderId: document.orderId,
  restaurantId: document.restaurantId,
  items: document.items.map((item) => ({
    menuItemId: item.menuItemId,
    quantity: item.quantity,
    options: [...item.options],
  })),
  state: document.state,
  timeline: { ...document.timeline },
  rejectionReason: document.rejectionReason,
  failureReason: document.failureReason,
  cancellation:
    document.cancellation === undefined
      ? undefined
      : { ...document.cancellation },
});

export const createRestaurantOrderCollection = (
  db: Db,
  unitOfWork: MongoUnitOfWork,
): VersionedCollection<RestaurantOrderDocument> =>
  new VersionedCollection<RestaurantOrderDocument>({
    db,
    name: RESTAURANT_ORDERS_COLLECTION,
    aggregateType: "RestaurantOrder",
    unitOfWork,
    duplicateKey: (keyPattern, document) =>
      "orderId" in keyPattern
        ? new DuplicateOrderIdError(document.orderId)
        : undefined,
  });

export class MongoRestaurantOrderRepository
  implements RestaurantOrderRepository
{
  readonly #collection: VersionedCollection<RestaurantOrderDocument>;

  constructor(collection: VersionedCollection<RestaurantOrderDocument>) {
    this.#collection = collection;
  }

  async get(id: string): Promise<RestaurantOrder | undefined> {
    return this.#restore(await this.#collection.findOne({ _id: id }));
  }

  async findByOrderId(orderId: string): Promise<RestaurantOrder | undefined> {
    return this.#restore(await this.#collection.findOne({ orderId }));
  }

  async save(order: RestaurantOrder): Promise<number> {
    const snapshot = order.toSnapshot();
    return this.#collection.save(order.id, order.version, {
      schemaVersion: RESTAURANT_ORDER_SCHEMA_VERSION,
      orderId: snapshot.orderId,
      restaurantId: snapshot.restaurantId,
      items: snapshot.items.map((item) => ({
        menuItemId: item.menuItemId,
        quantity: item.quantity,
        options: [...item.options],
      })),
      state: snapshot.state,
      timeline: { ...snapshot.timeline },
      rejectionReason: snapshot.rejectionReason,
      failureReason: snapshot.failureReason,
      cancellation:
        snapshot.cancellation === undefined
          ? undefined
          : { ...snapshot.cancellation },
    });
  }

  #restore(
    document: RestaurantOrderDocument | undefined,
  ): RestaurantOrder | undefined {
    return document === undefined
      ? undefined
      : RestaurantOrder.restore(toSnapshot(document), document.version);
  }
}

const boardItems = (document: RestaurantOrderDocument) =>
  document.items.map((item) => ({
    menuItemId: item.menuItemId,
    quantity: item.quantity,
    options: [...item.options],
  }));

/**
 * Kitchen board read models queried from the restaurant order documents via
 * the `(restaurantId, state, …)` indexes.
 */
export class MongoKitchenBoard
  implements
    RestaurantOrderReader,
    KitchenQueueReader,
    ActivePreparationsReader,
    ReadyOrdersReader
{
  readonly #collection: VersionedCollection<RestaurantOrderDocument>;

  constructor(collection: VersionedCollection<RestaurantOrderDocument>) {
    this.#collection = collection;
  }

  async get(
    restaurantId: string,
    restaurantOrderId: string,
  ): Promise<RestaurantOrderView | undefined> {
    const document = await this.#collection.findOne({
      _id: restaurantOrderId,
      restaurantId,
    });
    return document === undefined
      ? undefined
      : snapshotToView(toSnapshot(document), document.version);
  }

  async listQueued(
    restaurantId: string,
  ): Promise<readonly KitchenQueueEntry[]> {
    const documents = await this.#collection.find(
      { restaurantId, state: RestaurantOrderState.Queued },
      { sort: { _id: 1 } },
    );
    return documents.map((document) => ({
      restaurantOrderId: document._id,
      orderId: document.orderId,
      items: boardItems(document),
      acceptedAt: document.timeline.acceptedAt,
      queuedAt: document.timeline.queuedAt,
      expectedReadyAt: document.timeline.expectedReadyAt,
    }));
  }

  async listActive(
    restaurantId: string,
  ): Promise<readonly ActivePreparation[]> {
    const documents = await this.#collection.find(
      {
        restaurantId,
        state: {
          $in: [RestaurantOrderState.Preparing, RestaurantOrderState.Assembled],
        },
      },
      { sort: { "timeline.preparationStartedAt": 1, _id: 1 } },
    );
    return documents.map((document) => ({
      restaurantOrderId: document._id,
      orderId: document.orderId,
      state: document.state,
      items: boardItems(document),
      preparationStartedAt: document.timeline.preparationStartedAt,
      expectedReadyAt: document.timeline.expectedReadyAt,
    }));
  }

  async listReady(restaurantId: string): Promise<readonly ReadyOrder[]> {
    const documents = await this.#collection.find(
      { restaurantId, state: RestaurantOrderState.Ready },
      { sort: { "timeline.readyAt": 1, _id: 1 } },
    );
    return documents.map((document) => ({
      restaurantOrderId: document._id,
      orderId: document.orderId,
      readyAt: document.timeline.readyAt,
    }));
  }
}

const optionalDate = { bsonType: "date" };

export const restaurantOrderCollectionSpec: CollectionSpec = {
  name: RESTAURANT_ORDERS_COLLECTION,
  validator: {
    $jsonSchema: {
      bsonType: "object",
      required: [
        "_id",
        "version",
        "schemaVersion",
        "orderId",
        "restaurantId",
        "items",
        "state",
        "timeline",
      ],
      properties: {
        _id: { bsonType: "string", minLength: 1 },
        version: { bsonType: ["int", "long"], minimum: 1 },
        schemaVersion: { bsonType: "int", minimum: 1 },
        orderId: { bsonType: "string", minLength: 1 },
        restaurantId: { bsonType: "string", minLength: 1 },
        items: {
          bsonType: "array",
          minItems: 1,
          items: {
            bsonType: "object",
            required: ["menuItemId", "quantity", "options"],
            properties: {
              menuItemId: { bsonType: "string", minLength: 1 },
              quantity: { bsonType: ["int", "long"], minimum: 1 },
              options: { bsonType: "array", items: { bsonType: "string" } },
            },
          },
        },
        state: { enum: Object.values(RestaurantOrderState) },
        timeline: {
          bsonType: "object",
          properties: {
            acceptedAt: optionalDate,
            queuedAt: optionalDate,
            preparationStartedAt: optionalDate,
            expectedReadyAt: optionalDate,
            readyAt: optionalDate,
          },
        },
        rejectionReason: { bsonType: "string" },
        failureReason: { bsonType: "string" },
        cancellation: {
          bsonType: "object",
          required: ["stage"],
          properties: {
            stage: { enum: Object.values(RestaurantOrderState) },
            reason: { bsonType: "string" },
          },
        },
      },
    },
  },
  indexes: [
    {
      key: { orderId: 1 },
      name: "restaurant_orders_order_unique",
      unique: true,
    },
    // Kitchen board: queue, active preparations and ready orders per restaurant.
    {
      key: { restaurantId: 1, state: 1, "timeline.preparationStartedAt": 1 },
      name: "restaurant_orders_board_started",
    },
    {
      key: { restaurantId: 1, state: 1, "timeline.readyAt": 1 },
      name: "restaurant_orders_board_ready",
    },
  ],
};

import type { Db } from "mongodb";

import { ConcurrencyConflictError } from "../../../../pkg/application/errors.ts";
import type { DatabaseUnitOfWork } from "../../../../pkg/infrastructure/repository/unit-of-work.ts";
import {
  VersionedCollection,
  type VersionedDocument,
} from "../../../../pkg/infrastructure/repository/versioned-collection.ts";
import type { CookWorkload } from "../../../cook/applications/cook-workload.ts";
import type {
  PendingKitchenOrder,
  PendingKitchenOrdersReader,
} from "../../../simulator/applications/simulate-kitchen/simulate-kitchen.ts";
import { SIMULATED_STATES } from "../../../simulator/domains/simulator/simulator.ts";
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
  ActiveRestaurantOrder,
  ActiveRestaurantOrdersReader,
} from "../../applications/list-active-restaurant-orders/list-active-restaurant-orders.ts";
import type {
  IncomingOrder,
  IncomingOrdersReader,
} from "../../applications/list-incoming-orders/list-incoming-orders.ts";
import type {
  ReadyOrder,
  ReadyOrdersReader,
} from "../../applications/list-ready-orders/list-ready-orders.ts";
import type {
  ScheduledOrder,
  ScheduledOrdersReader,
} from "../../applications/list-scheduled-orders/list-scheduled-orders.ts";
import {
  type RestaurantOrderView,
  snapshotToView,
} from "../../applications/restaurant-order-view.ts";
import type { StartedOrders } from "../../applications/start-preparation/start-preparation.ts";
import {
  DuplicateOrderIdError,
  leavesOf,
  type OrderPriority,
  RestaurantOrder,
  RestaurantOrderCompoundState,
  type RestaurantOrderRepository,
  type RestaurantOrderSnapshot,
  RestaurantOrderState,
} from "../../domains/restaurantorder/restaurantorder.ts";
import type { KitchenChangeNotifier } from "../integration/kitchen-changes.ts";

export const RESTAURANT_ORDERS_COLLECTION = "restaurant_orders";
export const RESTAURANT_ORDER_SCHEMA_VERSION = 6;

/**
 * One document per Restaurant Order with its Preparation Items and timeline
 * embedded (bounded by one order). It references Restaurant and the `orders`
 * Order only by id; `orderId` is unique (idempotent Receive). `marketId` is
 * the Market of the restaurant that received it (ADR 41, required since
 * schemaVersion 3).
 */
export type RestaurantOrderDocument = VersionedDocument & {
  orderId: string;
  restaurantId: string;
  marketId: string;
  items: { menuItemId: string; quantity: number; options: string[] }[];
  state: RestaurantOrderState;
  timeline: {
    receivedAt?: Date;
    acceptedAt?: Date;
    queuedAt?: Date;
    preparationStartedAt?: Date;
    expectedReadyAt?: Date;
    readyAt?: Date;
    /** Since schemaVersion 4. */
    pickedUpAt?: Date;
  };
  /** Since schemaVersion 5 (FR-651); orders received before are `STANDARD`. */
  priority: OrderPriority;
  /** Since schemaVersion 6: schedule of a scheduled order (FR-683). */
  schedule?: { windowStart: Date; windowEnd: Date; plannedReadyAt: Date };
  /** Since schemaVersion 2; required in `PREPARING` and `ASSEMBLED`. */
  cookId?: string;
  /** Since schemaVersion 5: request_id of the Start Next Preparation. */
  startRequestId?: string;
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
  marketId: document.marketId,
  items: document.items.map((item) => ({
    menuItemId: item.menuItemId,
    quantity: item.quantity,
    options: [...item.options],
  })),
  state: document.state,
  timeline: { ...document.timeline },
  priority: document.priority,
  schedule:
    document.schedule === undefined ? undefined : { ...document.schedule },
  cookId: document.cookId,
  startRequestId: document.startRequestId,
  rejectionReason: document.rejectionReason,
  failureReason: document.failureReason,
  cancellation:
    document.cancellation === undefined
      ? undefined
      : { ...document.cancellation },
});

export const createRestaurantOrderCollection = (
  db: Db,
  unitOfWork: DatabaseUnitOfWork,
): VersionedCollection<RestaurantOrderDocument> =>
  new VersionedCollection<RestaurantOrderDocument>({
    db,
    name: RESTAURANT_ORDERS_COLLECTION,
    aggregateType: "RestaurantOrder",
    unitOfWork,
    // Two active orders of one cook (FR-617) and two orders started by one
    // Start Next Preparation request lose like a concurrent write.
    duplicateKey: (keyPattern, document) =>
      "orderId" in keyPattern
        ? new DuplicateOrderIdError(document.orderId)
        : "cookId" in keyPattern || "startRequestId" in keyPattern
          ? new ConcurrencyConflictError("RestaurantOrder", document._id)
          : undefined,
  });

export class DatabaseRestaurantOrderRepository
  implements RestaurantOrderRepository
{
  readonly #collection: VersionedCollection<RestaurantOrderDocument>;
  readonly #kitchenChanged: KitchenChangeNotifier;

  /** `kitchenChanged` runs in the same Unit of Work as every save (ADR 36). */
  constructor(
    collection: VersionedCollection<RestaurantOrderDocument>,
    kitchenChanged: KitchenChangeNotifier,
  ) {
    this.#collection = collection;
    this.#kitchenChanged = kitchenChanged;
  }

  async get(id: string): Promise<RestaurantOrder | undefined> {
    return this.#restore(await this.#collection.findOne({ _id: id }));
  }

  async findByOrderId(orderId: string): Promise<RestaurantOrder | undefined> {
    return this.#restore(await this.#collection.findOne({ orderId }));
  }

  async save(order: RestaurantOrder): Promise<number> {
    const snapshot = order.toSnapshot();
    return this.#kitchenChanged({
      type: "RestaurantOrder",
      id: order.id,
      restaurantId: snapshot.restaurantId,
      save: () =>
        this.#collection.save(order.id, order.version, {
          schemaVersion: RESTAURANT_ORDER_SCHEMA_VERSION,
          orderId: snapshot.orderId,
          restaurantId: snapshot.restaurantId,
          marketId: snapshot.marketId,
          items: snapshot.items.map((item) => ({
            menuItemId: item.menuItemId,
            quantity: item.quantity,
            options: [...item.options],
          })),
          state: snapshot.state,
          timeline: { ...snapshot.timeline },
          priority: snapshot.priority,
          schedule:
            snapshot.schedule === undefined
              ? undefined
              : { ...snapshot.schedule },
          cookId: snapshot.cookId,
          startRequestId: snapshot.startRequestId,
          rejectionReason: snapshot.rejectionReason,
          failureReason: snapshot.failureReason,
          cancellation:
            snapshot.cancellation === undefined
              ? undefined
              : { ...snapshot.cancellation },
        }),
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

const scheduleOf = (document: RestaurantOrderDocument) =>
  document.schedule === undefined ? undefined : { ...document.schedule };

/**
 * Kitchen board read models queried from the restaurant order documents via
 * the `(restaurantId, state, …)` indexes.
 */
export class DatabaseKitchenBoard
  implements
    RestaurantOrderReader,
    IncomingOrdersReader,
    KitchenQueueReader,
    ActivePreparationsReader,
    ReadyOrdersReader,
    ActiveRestaurantOrdersReader,
    ScheduledOrdersReader,
    PendingKitchenOrdersReader,
    CookWorkload,
    StartedOrders
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

  /** The order a Start Next Preparation `requestId` started (FR-225). */
  async findStarted(
    restaurantId: string,
    requestId: string,
  ): Promise<RestaurantOrderView | undefined> {
    const document = await this.#collection.findOne({
      restaurantId,
      startRequestId: requestId,
    });
    return document === undefined
      ? undefined
      : snapshotToView(toSnapshot(document), document.version);
  }

  async listIncoming(restaurantId: string): Promise<readonly IncomingOrder[]> {
    const documents = await this.#collection.find(
      { restaurantId, state: RestaurantOrderState.Received },
      { sort: { "timeline.receivedAt": 1, _id: 1 } },
    );
    return documents.map((document) => ({
      restaurantOrderId: document._id,
      orderId: document.orderId,
      items: boardItems(document),
      receivedAt: document.timeline.receivedAt,
      version: document.version,
    }));
  }

  async listActiveOrders(
    restaurantId: string,
  ): Promise<readonly ActiveRestaurantOrder[]> {
    const documents = await this.#collection.find(
      {
        restaurantId,
        state: {
          $in: [
            RestaurantOrderState.Received,
            RestaurantOrderState.Queued,
            RestaurantOrderState.Preparing,
            RestaurantOrderState.Assembled,
            RestaurantOrderState.Ready,
          ],
        },
      },
      { sort: { "timeline.receivedAt": 1, _id: 1 } },
    );
    return documents.map((document) => ({
      restaurantOrderId: document._id,
      orderId: document.orderId,
      state: document.state,
      priority: document.priority,
      items: boardItems(document),
      receivedAt: document.timeline.receivedAt,
      acceptedAt: document.timeline.acceptedAt,
      expectedReadyAt: document.timeline.expectedReadyAt,
      cookId: document.cookId,
      schedule: scheduleOf(document),
      version: document.version,
    }));
  }

  /**
   * Accepted scheduled orders waiting for their start (FR-684), via the
   * `(restaurantId, state, schedule.plannedReadyAt)` index.
   */
  async listScheduled(
    restaurantId: string,
  ): Promise<readonly ScheduledOrder[]> {
    const documents = await this.#collection.find(
      {
        restaurantId,
        state: RestaurantOrderState.Accepted,
        schedule: { $exists: true },
      },
      { sort: { "schedule.plannedReadyAt": 1, _id: 1 } },
    );
    return documents.flatMap((document) =>
      document.schedule === undefined
        ? []
        : [
            {
              restaurantOrderId: document._id,
              orderId: document.orderId,
              items: boardItems(document),
              schedule: { ...document.schedule },
              priority: document.priority,
              version: document.version,
            },
          ],
    );
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
      state: document.state,
      priority: document.priority,
      items: boardItems(document),
      acceptedAt: document.timeline.acceptedAt,
      queuedAt: document.timeline.queuedAt,
      expectedReadyAt: document.timeline.expectedReadyAt,
      schedule: scheduleOf(document),
      version: document.version,
    }));
  }

  async listActive(
    restaurantId: string,
  ): Promise<readonly ActivePreparation[]> {
    const documents = await this.#collection.find(
      {
        restaurantId,
        state: {
          $in: [...leavesOf(RestaurantOrderCompoundState.ActivePreparation)],
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
      cookId: document.cookId,
    }));
  }

  /** Cook Pool (FR-617): cooks holding an order in Active Preparation. */
  async activeAssignments(
    restaurantId: string,
  ): Promise<ReadonlyMap<string, string>> {
    const documents = await this.#collection.find(
      {
        restaurantId,
        state: {
          $in: [...leavesOf(RestaurantOrderCompoundState.ActivePreparation)],
        },
      },
      { projection: { _id: 1, cookId: 1 } },
    );
    const assignments = new Map<string, string>();
    for (const document of documents) {
      if (document.cookId !== undefined) {
        assignments.set(document.cookId, document._id);
      }
    }
    return assignments;
  }

  /** Backlog of the Cook Pool: orders of the restaurant in `QUEUED`. */
  async queuedOrders(restaurantId: string): Promise<number> {
    return this.#collection.count({
      restaurantId,
      state: RestaurantOrderState.Queued,
    });
  }

  /**
   * Demo Simulator (FR-605): orders of every restaurant waiting for the
   * kitchen, via the `(state, receivedAt)` index.
   */
  async listPendingKitchenOrders(): Promise<readonly PendingKitchenOrder[]> {
    const documents = await this.#collection.find(
      { state: { $in: [...SIMULATED_STATES] } },
      { sort: { state: 1, "timeline.receivedAt": 1, _id: 1 } },
    );
    return documents.map((document) => ({
      restaurantOrderId: document._id,
      restaurantId: document.restaurantId,
      marketId: document.marketId,
      state: document.state as PendingKitchenOrder["state"],
      expectedReadyAt: document.timeline.expectedReadyAt,
      scheduled: document.schedule !== undefined,
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

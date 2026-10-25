import type { Subject } from "../../../../pkg/application/subject.ts";
import {
  InMemoryDocumentStore,
  type StoredDocument,
} from "../../../../pkg/infrastructure/memory/document-store.ts";
import type {
  AcceptanceConditionsProvider,
  ReadyTimeEstimator,
} from "../../applications/accept-order/accept-order.ts";
import type {
  FulfillmentCoordinatorAccess,
  KitchenStaffAccess,
  RestaurantOrderViewAccess,
} from "../../applications/access-control.ts";
import type {
  KitchenQueueEntry,
  KitchenQueueOrdering,
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
} from "../../applications/restaurant-order-view.ts";
import type { AcceptanceConditions } from "../../domains/restaurantorder/acceptance.ts";
import {
  DuplicateOrderIdError,
  RestaurantOrder,
  type RestaurantOrderRepository,
  type RestaurantOrderSnapshot,
  RestaurantOrderState,
} from "../../domains/restaurantorder/restaurantorder.ts";

export const createRestaurantOrderStore =
  (): InMemoryDocumentStore<RestaurantOrderSnapshot> =>
    new InMemoryDocumentStore<RestaurantOrderSnapshot>("RestaurantOrder");

/** Enforces unique `orderId` like a unique index of the future collection. */
export class InMemoryRestaurantOrderRepository
  implements RestaurantOrderRepository
{
  readonly #store: InMemoryDocumentStore<RestaurantOrderSnapshot>;

  constructor(store: InMemoryDocumentStore<RestaurantOrderSnapshot>) {
    this.#store = store;
  }

  async get(id: string): Promise<RestaurantOrder | undefined> {
    const stored = this.#store.get(id);
    return stored === undefined
      ? undefined
      : RestaurantOrder.restore(stored.document, stored.version);
  }

  async findByOrderId(orderId: string): Promise<RestaurantOrder | undefined> {
    const stored = this.#store
      .all()
      .find((candidate) => candidate.document.orderId === orderId);
    return stored === undefined
      ? undefined
      : RestaurantOrder.restore(stored.document, stored.version);
  }

  async save(order: RestaurantOrder): Promise<number> {
    const duplicate = this.#store
      .all()
      .some(
        (candidate) =>
          candidate.document.orderId === order.orderId &&
          candidate.id !== order.id,
      );
    if (duplicate) {
      throw new DuplicateOrderIdError(order.orderId);
    }
    return this.#store.save(order.id, order.version, order.toSnapshot());
  }
}

const byTime =
  (time: (snapshot: RestaurantOrderSnapshot) => Date | undefined) =>
  (
    left: StoredDocument<RestaurantOrderSnapshot>,
    right: StoredDocument<RestaurantOrderSnapshot>,
  ): number =>
    (time(left.document)?.getTime() ?? 0) -
      (time(right.document)?.getTime() ?? 0) ||
    (left.id < right.id ? -1 : left.id > right.id ? 1 : 0);

const boardItems = (snapshot: RestaurantOrderSnapshot) =>
  snapshot.items.map((item) => ({
    menuItemId: item.menuItemId,
    quantity: item.quantity,
    options: [...item.options],
  }));

/**
 * Kitchen board read models projected on the fly from the in-memory
 * documents. A production read model is updated from Domain Events instead.
 */
export class InMemoryKitchenBoard
  implements
    RestaurantOrderReader,
    KitchenQueueReader,
    ActivePreparationsReader,
    ReadyOrdersReader
{
  readonly #store: InMemoryDocumentStore<RestaurantOrderSnapshot>;

  constructor(store: InMemoryDocumentStore<RestaurantOrderSnapshot>) {
    this.#store = store;
  }

  async get(
    restaurantId: string,
    restaurantOrderId: string,
  ): Promise<RestaurantOrderView | undefined> {
    const stored = this.#store.get(restaurantOrderId);
    return stored === undefined || stored.document.restaurantId !== restaurantId
      ? undefined
      : snapshotToView(stored.document, stored.version);
  }

  async listQueued(
    restaurantId: string,
  ): Promise<readonly KitchenQueueEntry[]> {
    return this.#inStates(restaurantId, [RestaurantOrderState.Queued]).map(
      ({ document }) => ({
        restaurantOrderId: document.id,
        orderId: document.orderId,
        items: boardItems(document),
        acceptedAt: document.timeline.acceptedAt,
        queuedAt: document.timeline.queuedAt,
        expectedReadyAt: document.timeline.expectedReadyAt,
      }),
    );
  }

  async listActive(
    restaurantId: string,
  ): Promise<readonly ActivePreparation[]> {
    return this.#inStates(restaurantId, [
      RestaurantOrderState.Preparing,
      RestaurantOrderState.Assembled,
    ])
      .sort(byTime((document) => document.timeline.preparationStartedAt))
      .map(({ document }) => ({
        restaurantOrderId: document.id,
        orderId: document.orderId,
        state: document.state,
        items: boardItems(document),
        preparationStartedAt: document.timeline.preparationStartedAt,
        expectedReadyAt: document.timeline.expectedReadyAt,
      }));
  }

  async listReady(restaurantId: string): Promise<readonly ReadyOrder[]> {
    return this.#inStates(restaurantId, [RestaurantOrderState.Ready])
      .sort(byTime((document) => document.timeline.readyAt))
      .map(({ document }) => ({
        restaurantOrderId: document.id,
        orderId: document.orderId,
        readyAt: document.timeline.readyAt,
      }));
  }

  #inStates(
    restaurantId: string,
    states: readonly RestaurantOrderState[],
  ): StoredDocument<RestaurantOrderSnapshot>[] {
    return this.#store
      .all()
      .filter(
        ({ document }) =>
          document.restaurantId === restaurantId &&
          states.includes(document.state),
      );
  }
}

/**
 * Test-only queue policy: FIFO by `accepted_at`, then by id. It is not a
 * product decision about the Kitchen Queue order.
 */
export class FifoKitchenQueueOrdering implements KitchenQueueOrdering {
  order(entries: readonly KitchenQueueEntry[]): readonly KitchenQueueEntry[] {
    return [...entries].sort(
      (left, right) =>
        (left.acceptedAt?.getTime() ?? 0) -
          (right.acceptedAt?.getTime() ?? 0) ||
        (left.restaurantOrderId < right.restaurantOrderId ? -1 : 1),
    );
  }
}

/**
 * Test conditions: an always-open schedule with spare capacity unless the
 * test overrides the restaurant snapshot or a Menu Item offer.
 */
export class StaticAcceptanceConditions
  implements AcceptanceConditionsProvider
{
  restaurant: AcceptanceConditions["restaurant"] = {
    opensAt: new Date(0),
    closesAt: new Date(8.64e15),
    currentOrders: 0,
    maximumOrders: Number.MAX_SAFE_INTEGER,
    scheduleException: false,
  };
  readonly offers = new Map<
    string,
    AcceptanceConditions["menuItems"][string]
  >();

  async load(input: {
    restaurantId: string;
    menuItemIds: readonly string[];
    now: Date;
  }): Promise<AcceptanceConditions> {
    const menuItems: Record<string, AcceptanceConditions["menuItems"][string]> =
      {};
    for (const menuItemId of input.menuItemIds) {
      menuItems[menuItemId] = this.offers.get(menuItemId) ?? {
        availableFrom: new Date(0),
        availableUntil: new Date(8.64e15),
        remainingCapacity: Number.MAX_SAFE_INTEGER,
      };
    }
    return { restaurant: this.restaurant, menuItems };
  }
}

/** Test estimator: a fixed preparation duration after Acceptance. */
export class FixedDurationReadyTimeEstimator implements ReadyTimeEstimator {
  readonly #milliseconds: number;

  constructor(milliseconds: number) {
    this.#milliseconds = milliseconds;
  }

  async estimate(input: { acceptedAt: Date }): Promise<Date> {
    return new Date(input.acceptedAt.getTime() + this.#milliseconds);
  }
}

/** Test authorization graph: restaurant staff and fulfillment services. */
export class InMemoryKitchenAccessControl
  implements
    KitchenStaffAccess,
    FulfillmentCoordinatorAccess,
    RestaurantOrderViewAccess
{
  readonly #staff = new Set<string>();
  readonly #fulfillment = new Set<string>();

  grantStaff(subject: Subject, restaurantId: string): this {
    this.#staff.add(`${subject.type}:${subject.id}@${restaurantId}`);
    return this;
  }

  grantFulfillment(subject: Subject, restaurantId: string): this {
    this.#fulfillment.add(`${subject.type}:${subject.id}@${restaurantId}`);
    return this;
  }

  async canOperateKitchen(
    subject: Subject,
    restaurantId: string,
  ): Promise<boolean> {
    return this.#staff.has(`${subject.type}:${subject.id}@${restaurantId}`);
  }

  async canCoordinateFulfillment(
    subject: Subject,
    restaurantId: string,
  ): Promise<boolean> {
    return (
      subject.type === "service" &&
      this.#fulfillment.has(`${subject.type}:${subject.id}@${restaurantId}`)
    );
  }

  async canViewRestaurantOrders(
    subject: Subject,
    restaurantId: string,
  ): Promise<boolean> {
    return (
      (await this.canOperateKitchen(subject, restaurantId)) ||
      (await this.canCoordinateFulfillment(subject, restaurantId))
    );
  }
}

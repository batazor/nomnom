import { requirePermission } from "../../../../../pkg/authz/require-permission.ts";
import type { Subject } from "../../../../../pkg/authz/subject.ts";
import type { Outbox } from "../../../../../pkg/outbox/outbox.ts";
import type { UnitOfWork } from "../../../../../pkg/unit-of-work/unit-of-work.ts";
import type { Clock } from "../../../../../pkg/usecase/clock.ts";
import {
  AlreadyInStateError,
  InvalidRestaurantOrderError,
  InvalidRestaurantOrderReason,
  type RestaurantOrderRepository,
  RestaurantOrderState,
} from "../../../domains/restaurantorder/restaurantorder.ts";
import type { KitchenStaffAccess } from "../../access-control/access-control.ts";
import type {
  KitchenQueueOrdering,
  KitchenQueueReader,
} from "../../kitchen-board/get-queue/get-queue.ts";
import type { RepeatPolicy } from "../../kitchen-command/kitchen-command.ts";
import { loadRestaurantOrder } from "../../load/load.ts";
import { saveAndPublish } from "../../publish-change/publish-change.ts";
import {
  type RestaurantOrderView,
  toRestaurantOrderView,
} from "../../restaurant-order-view/restaurant-order-view.ts";

/**
 * Start Next Preparation: the kitchen names the restaurant, the Kitchen Queue
 * names the order. `requestId` is chosen per attempt and reused on a retry.
 */
export type StartPreparation = Readonly<{
  subject: Subject;
  restaurantId: string;
  requestId: string;
}>;

/**
 * Cook Pool of the restaurant as seen by Start Preparation: assigns a free
 * cook on line inside the active Unit of Work and returns its id, or throws
 * `NoCookAvailableError`.
 */
export interface CookAssigner {
  assignFreeCook(restaurantId: string, at: Date): Promise<string>;
}

/** The order a Start Next Preparation `requestId` already started. */
export interface StartedOrders {
  findStarted(
    restaurantId: string,
    requestId: string,
  ): Promise<RestaurantOrderView | undefined>;
}

/**
 * Starts Preparation of the head of the Kitchen Queue by a free cook on line
 * and publishes `PreparationStarted` (FR-225). The kitchen does not choose the
 * order: the Kitchen Queue policy does. Without a free cook the queue stays as
 * it was with `NoCookAvailableError`. An empty queue is an empty result, not
 * an error: nothing is started. A repeat with the same `requestId` starts no
 * other order and follows the explicit {@link RepeatPolicy}.
 */
export class StartPreparationHandler {
  readonly #accessControl: KitchenStaffAccess;
  readonly #unitOfWork: UnitOfWork;
  readonly #restaurantOrders: RestaurantOrderRepository;
  readonly #started: StartedOrders;
  readonly #queue: KitchenQueueReader;
  readonly #ordering: KitchenQueueOrdering;
  readonly #cooks: CookAssigner;
  readonly #outbox: Outbox;
  readonly #clock: Clock;
  readonly #onRepeat: RepeatPolicy;

  constructor(dependencies: {
    accessControl: KitchenStaffAccess;
    unitOfWork: UnitOfWork;
    restaurantOrders: RestaurantOrderRepository;
    started: StartedOrders;
    queue: KitchenQueueReader;
    ordering: KitchenQueueOrdering;
    cooks: CookAssigner;
    outbox: Outbox;
    clock: Clock;
    onRepeat: RepeatPolicy;
  }) {
    this.#accessControl = dependencies.accessControl;
    this.#unitOfWork = dependencies.unitOfWork;
    this.#restaurantOrders = dependencies.restaurantOrders;
    this.#started = dependencies.started;
    this.#queue = dependencies.queue;
    this.#ordering = dependencies.ordering;
    this.#cooks = dependencies.cooks;
    this.#outbox = dependencies.outbox;
    this.#clock = dependencies.clock;
    this.#onRepeat = dependencies.onRepeat;
  }

  /** The started order; `undefined` when the Kitchen Queue is empty. */
  async execute(
    command: StartPreparation,
  ): Promise<RestaurantOrderView | undefined> {
    await requirePermission(
      this.#accessControl.canOperateKitchen(
        command.subject,
        command.restaurantId,
      ),
      "start preparation",
    );
    const requestId = command.requestId.trim();
    if (requestId.length === 0) {
      throw new InvalidRestaurantOrderError(
        InvalidRestaurantOrderReason.EmptyStartRequestId,
      );
    }
    const now = this.#clock.now();
    return this.#unitOfWork.run(async () => {
      const repeated = await this.#started.findStarted(
        command.restaurantId,
        requestId,
      );
      if (repeated !== undefined) {
        if (this.#onRepeat === "succeed-without-event") return repeated;
        throw new AlreadyInStateError(RestaurantOrderState.Preparing);
      }
      const queued = await this.#queue.listQueued(command.restaurantId);
      const [head] = await this.#ordering.order(
        command.restaurantId,
        queued,
        now,
      );
      if (head === undefined) return undefined;
      const order = await loadRestaurantOrder(
        this.#restaurantOrders,
        command.restaurantId,
        head.restaurantOrderId,
      );
      order.checkStartPreparation();
      const cookId = await this.#cooks.assignFreeCook(order.restaurantId, now);
      order.startPreparation({ startedAt: now, cookId, requestId });
      const version = await saveAndPublish(
        this.#restaurantOrders,
        this.#outbox,
        order,
        ["PreparationStarted"],
      );
      return toRestaurantOrderView(order, version);
    });
  }
}

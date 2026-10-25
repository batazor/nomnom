// Change Notifications of the kitchen board (ADR 36): every write of a
// Restaurant Order or of its Restaurant changes Kitchen(restaurantId) in the
// BFF, so the Router pushes kitchenChanged to its subscribers. No state: the
// Router re-reads the board with the subscriber's rights.
import type {
  AggregateRef,
  ChangeNotification,
  ChangeNotifications,
} from "../../../../pkg/application/outbox.ts";
import type { UnitOfWork } from "../../../../pkg/application/unit-of-work.ts";

export const KITCHEN_CHANGES_TOPIC = "restaurants.v1.kitchen-changes";

export const kitchenChanged = (
  restaurantId: string,
  occurredAt: Date,
): ChangeNotification => ({
  type: "KitchenChanged",
  topic: KITCHEN_CHANGES_TOPIC,
  entity: { __typename: "Kitchen", restaurantId },
  occurredAt,
});

/** A write of one aggregate: returns its committed version. */
export type AggregateWrite = Readonly<{
  type: string;
  id: string;
  restaurantId: string;
  save: () => Promise<number>;
}>;

/**
 * Runs the write and stores the notification of its kitchen in one Unit of
 * Work: joins the active one, or opens its own for use cases that save a
 * single aggregate without one, so the notification never misses a commit.
 */
export type KitchenChangeNotifier = (write: AggregateWrite) => Promise<number>;

export const kitchenChangeNotifier =
  (
    unitOfWork: UnitOfWork,
    notifications: ChangeNotifications,
    now: () => Date,
  ): KitchenChangeNotifier =>
  (write) =>
    unitOfWork.run(async () => {
      const version = await write.save();
      const aggregate: AggregateRef = {
        type: write.type,
        id: write.id,
        version,
      };
      await notifications.notify(
        aggregate,
        kitchenChanged(write.restaurantId, now()),
      );
      return version;
    });

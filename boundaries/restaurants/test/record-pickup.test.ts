import assert from "node:assert/strict";
import test from "node:test";
import { RecordPickupHandler } from "../slices/restaurantorder/applications/preparation/record-pickup/record-pickup.ts";
import { RestaurantOrderState } from "../slices/restaurantorder/domains/restaurantorder/restaurantorder.ts";
import { cook, createKitchenWorld } from "./support/kitchen.ts";
import { createInfrastructure } from "./support/restaurants.ts";

const PICKED_UP_AT = new Date("2026-10-26T16:50:00.000Z");
const WORKFLOW = { type: "service", id: "orders" } as const;

const setup = async () => {
  const infrastructure = await createInfrastructure();
  const world = await createKitchenWorld({ infrastructure });
  const kitchen = await world.givenKitchen();
  const recordPickup = new RecordPickupHandler({
    accessControl: {
      canCoordinateFulfillment: async (subject) =>
        subject.type === WORKFLOW.type && subject.id === WORKFLOW.id,
    },
    unitOfWork: infrastructure.unitOfWork,
    restaurantOrders: world.restaurantOrders,
  });
  return { world, kitchen, recordPickup };
};

test("the Pickup takes a READY order to PICKED_UP and off the kitchen board", async () => {
  const { world, kitchen, recordPickup } = await setup();
  const ready = await world.givenOrderIn(
    "READY",
    kitchen.restaurantId,
    kitchen.phoId,
  );

  await recordPickup.execute({
    subject: WORKFLOW,
    orderId: ready.orderId,
    pickedUpAt: PICKED_UP_AT,
  });

  const snapshot = (
    await world.restaurantOrders.findByOrderId(ready.orderId)
  )?.toSnapshot();
  assert.equal(snapshot?.state, RestaurantOrderState.PickedUp);
  assert.deepEqual(snapshot?.timeline.pickedUpAt, PICKED_UP_AT);
  const board = await world.listReadyOrders.execute({
    subject: cook,
    restaurantId: kitchen.restaurantId,
  });
  assert.deepEqual(board.orders, []);
});

test("a repeated Pickup changes nothing", async () => {
  const { world, kitchen, recordPickup } = await setup();
  const ready = await world.givenOrderIn(
    "READY",
    kitchen.restaurantId,
    kitchen.phoId,
  );
  const command = {
    subject: WORKFLOW,
    orderId: ready.orderId,
    pickedUpAt: PICKED_UP_AT,
  };
  await recordPickup.execute(command);
  const version = (await world.restaurantOrders.findByOrderId(ready.orderId))
    ?.version;

  await recordPickup.execute(command);

  const order = await world.restaurantOrders.findByOrderId(ready.orderId);
  assert.equal(order?.version, version);
});

test("a Pickup before READY is rejected and leaves the order in its state", async () => {
  const { world, kitchen, recordPickup } = await setup();
  const assembled = await world.givenOrderIn(
    "ASSEMBLED",
    kitchen.restaurantId,
    kitchen.phoId,
  );

  await assert.rejects(
    recordPickup.execute({
      subject: WORKFLOW,
      orderId: assembled.orderId,
      pickedUpAt: PICKED_UP_AT,
    }),
    { code: "invalid_transition" },
  );

  const snapshot = (
    await world.restaurantOrders.findByOrderId(assembled.orderId)
  )?.toSnapshot();
  assert.equal(snapshot?.state, RestaurantOrderState.Assembled);
});

test("an unknown order is not found", async () => {
  const { recordPickup } = await setup();

  await assert.rejects(
    recordPickup.execute({
      subject: WORKFLOW,
      orderId: "missing",
      pickedUpAt: PICKED_UP_AT,
    }),
    { code: "not_found" },
  );
});

test("only the workflow of orders may report the Pickup", async () => {
  const { world, kitchen, recordPickup } = await setup();
  const ready = await world.givenOrderIn(
    "READY",
    kitchen.restaurantId,
    kitchen.phoId,
  );

  await assert.rejects(
    recordPickup.execute({
      subject: cook,
      orderId: ready.orderId,
      pickedUpAt: PICKED_UP_AT,
    }),
    { code: "permission_denied" },
  );

  const snapshot = (
    await world.restaurantOrders.findByOrderId(ready.orderId)
  )?.toSnapshot();
  assert.equal(snapshot?.state, RestaurantOrderState.Ready);
});

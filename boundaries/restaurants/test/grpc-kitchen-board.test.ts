import assert from "node:assert/strict";
import test from "node:test";

import { timestampDate } from "@bufbuild/protobuf/wkt";
import { Code } from "@connectrpc/connect";

import { RestaurantOrderState } from "../infrastructure/grpc/gen/nomnom/restaurants/v1/service_pb.js";
import {
  context,
  createWorld,
  rejectsWith,
  resolverOf,
} from "./support/grpc.ts";
import { cook, minutes } from "./support/kitchen.ts";

test("AC-737 GetKitchenQueue lists the received and accepted order for kitchen staff", async () => {
  const { world, service } = createWorld({ subjects: resolverOf(cook) });
  const { restaurantId, phoId } = await world.givenKitchen();
  const other = await world.givenKitchen("Noodle Bar");
  const waiting = await world.givenOrderIn("RECEIVED", restaurantId, phoId);
  const first = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  world.clock.advance(minutes(1));
  const second = await world.givenOrderIn("QUEUED", restaurantId, phoId);
  await world.givenOrderIn("PREPARING", restaurantId, phoId);
  await world.givenOrderIn("QUEUED", other.restaurantId, other.phoId);

  const response = await service.getKitchenQueue({ restaurantId }, context);

  assert.deepEqual(
    response.entries.map((entry) => [
      entry.restaurantOrder?.restaurantOrderId,
      entry.restaurantOrder?.orderId,
    ]),
    [
      [first.restaurantOrderId, first.orderId],
      [second.restaurantOrderId, second.orderId],
    ],
  );
  assert.ok(
    response.entries.every(
      (entry) =>
        entry.restaurantOrder?.restaurantOrderId !== waiting.restaurantOrderId,
    ),
  );
  const [entry] = response.entries;
  const stored = await world.restaurantOrders.get(first.restaurantOrderId);
  assert.equal(entry?.restaurantOrder?.restaurantId, restaurantId);
  assert.equal(entry?.restaurantOrder?.state, RestaurantOrderState.QUEUED);
  assert.equal(entry?.restaurantOrder?.version, BigInt(stored?.version ?? 0));
  assert.ok(entry?.acceptedAt !== undefined && entry.queuedAt !== undefined);
  const expectedReadyAt = entry?.restaurantOrder?.expectedReadyAt;
  assert.ok(expectedReadyAt !== undefined);
  assert.ok(
    timestampDate(expectedReadyAt).getTime() >
      timestampDate(entry.acceptedAt).getTime(),
  );
});

test("AC-737 GetKitchenQueue of a restaurant with nothing queued is empty", async () => {
  const { world, service } = createWorld({ subjects: resolverOf(cook) });
  const { restaurantId, phoId } = await world.givenKitchen();
  await world.givenOrderIn("RECEIVED", restaurantId, phoId);

  const response = await service.getKitchenQueue({ restaurantId }, context);

  assert.deepEqual(response.entries, []);
});

test("AC-738 GetKitchenQueue without restaurant#operate_kitchen is PERMISSION_DENIED", async () => {
  const { world, service } = createWorld({
    subjects: resolverOf({ type: "user", id: "stranger" }),
  });
  const { restaurantId, phoId } = await world.givenKitchen();
  await world.givenOrderIn("QUEUED", restaurantId, phoId);

  await assert.rejects(
    async () => service.getKitchenQueue({ restaurantId }, context),
    rejectsWith(Code.PermissionDenied),
  );
});

test("AC-738 GetKitchenQueue without a verified identity is UNAUTHENTICATED", async () => {
  const { world, service } = createWorld({ subjects: null });
  const { restaurantId } = await world.givenKitchen();

  await assert.rejects(
    async () => service.getKitchenQueue({ restaurantId }, context),
    rejectsWith(Code.Unauthenticated),
  );
});

test("GetKitchenQueue without restaurant_id is INVALID_ARGUMENT", async () => {
  const { service } = createWorld({ subjects: resolverOf(cook) });

  await assert.rejects(
    async () => service.getKitchenQueue({ restaurantId: "" }, context),
    rejectsWith(Code.InvalidArgument),
  );
});

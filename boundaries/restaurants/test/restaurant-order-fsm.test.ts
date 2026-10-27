import assert from "node:assert/strict";
import test from "node:test";

import {
  ALL_STATES,
  ALL_TRIGGERS,
  AlreadyInStateError,
  GuardFailedError,
  InvalidTransitionError,
  REPEATS,
  type RestaurantOrderState,
  type RestaurantOrderTrigger,
  resolveTransition,
  RestaurantOrderState as S,
  TRANSITIONS,
  type TransitionRequest,
} from "../slices/restaurantorder/domains/restaurantorder/fsm.ts";

const at = (minute: number): Date =>
  new Date(Date.UTC(2026, 9, 25, 12, minute));

/** Inputs that satisfy every guard of the trigger. */
const passingRequest = (trigger: RestaurantOrderTrigger): TransitionRequest => {
  switch (trigger) {
    case "Accept":
      return { trigger, eligibility: { satisfied: true, reasons: [] } };
    case "ReportDelay":
      return {
        trigger,
        now: at(0),
        currentExpectedReadyAt: at(10),
        requestedExpectedReadyAt: at(20),
      };
    case "Cancel":
      return { trigger, policy: { duringPreparation: "allow" } };
    default:
      return { trigger } as TransitionRequest;
  }
};

type Expected = RestaurantOrderState | "already" | "invalid";

/** Complete expected matrix of `state × trigger`, written independently of the table. */
const EXPECTED: Record<
  RestaurantOrderState,
  Record<RestaurantOrderTrigger, Expected>
> = {
  RECEIVED: {
    Accept: S.Accepted,
    Reject: S.Rejected,
    Queue: "invalid",
    StartPreparation: "invalid",
    MarkAssembled: "invalid",
    MarkReady: "invalid",
    RecordPickup: "invalid",
    ReportDelay: "invalid",
    ReportFailure: "invalid",
    Cancel: S.Cancelled,
  },
  ACCEPTED: {
    Accept: "already",
    Reject: "invalid",
    Queue: S.Queued,
    StartPreparation: "invalid",
    MarkAssembled: "invalid",
    MarkReady: "invalid",
    RecordPickup: "invalid",
    ReportDelay: "invalid",
    ReportFailure: "invalid",
    Cancel: S.Cancelled,
  },
  QUEUED: {
    Accept: "invalid",
    Reject: "invalid",
    Queue: "already",
    StartPreparation: S.Preparing,
    MarkAssembled: "invalid",
    MarkReady: "invalid",
    RecordPickup: "invalid",
    ReportDelay: S.Queued,
    ReportFailure: "invalid",
    Cancel: S.Cancelled,
  },
  PREPARING: {
    Accept: "invalid",
    Reject: "invalid",
    Queue: "invalid",
    StartPreparation: "already",
    MarkAssembled: S.Assembled,
    MarkReady: "invalid",
    RecordPickup: "invalid",
    ReportDelay: S.Preparing,
    ReportFailure: S.Failed,
    Cancel: S.Cancelled,
  },
  ASSEMBLED: {
    Accept: "invalid",
    Reject: "invalid",
    Queue: "invalid",
    StartPreparation: "invalid",
    MarkAssembled: "already",
    MarkReady: S.Ready,
    RecordPickup: "invalid",
    ReportDelay: S.Assembled,
    ReportFailure: S.Failed,
    Cancel: S.Cancelled,
  },
  READY: {
    Accept: "invalid",
    Reject: "invalid",
    Queue: "invalid",
    StartPreparation: "invalid",
    MarkAssembled: "invalid",
    MarkReady: "already",
    RecordPickup: S.PickedUp,
    ReportDelay: "invalid",
    ReportFailure: "invalid",
    Cancel: "invalid",
  },
  PICKED_UP: {
    Accept: "invalid",
    Reject: "invalid",
    Queue: "invalid",
    StartPreparation: "invalid",
    MarkAssembled: "invalid",
    MarkReady: "invalid",
    RecordPickup: "already",
    ReportDelay: "invalid",
    ReportFailure: "invalid",
    Cancel: "invalid",
  },
  REJECTED: {
    Accept: "invalid",
    Reject: "already",
    Queue: "invalid",
    StartPreparation: "invalid",
    MarkAssembled: "invalid",
    MarkReady: "invalid",
    RecordPickup: "invalid",
    ReportDelay: "invalid",
    ReportFailure: "invalid",
    Cancel: "invalid",
  },
  CANCELLED: {
    Accept: "invalid",
    Reject: "invalid",
    Queue: "invalid",
    StartPreparation: "invalid",
    MarkAssembled: "invalid",
    MarkReady: "invalid",
    RecordPickup: "invalid",
    ReportDelay: "invalid",
    ReportFailure: "invalid",
    Cancel: "already",
  },
  FAILED: {
    Accept: "invalid",
    Reject: "invalid",
    Queue: "invalid",
    StartPreparation: "invalid",
    MarkAssembled: "invalid",
    MarkReady: "invalid",
    RecordPickup: "invalid",
    ReportDelay: "invalid",
    ReportFailure: "already",
    Cancel: "invalid",
  },
};

test("every state × trigger pair has the expected explicit outcome", () => {
  assert.equal(ALL_STATES.length, 10);
  assert.equal(ALL_TRIGGERS.length, 10);
  for (const state of ALL_STATES) {
    for (const trigger of ALL_TRIGGERS) {
      const expected = EXPECTED[state][trigger];
      const outcome = resolveTransition(state, passingRequest(trigger));
      const label = `${state} × ${trigger}`;
      if (expected === "already") {
        assert.ok(!outcome.ok, label);
        assert.ok(outcome.error instanceof AlreadyInStateError, label);
        assert.equal(outcome.error.state, state, label);
      } else if (expected === "invalid") {
        assert.ok(!outcome.ok, label);
        assert.ok(outcome.error instanceof InvalidTransitionError, label);
        assert.equal(outcome.error.from, state, label);
        assert.equal(outcome.error.trigger, trigger, label);
      } else {
        assert.deepEqual(
          outcome,
          { ok: true, from: state, trigger, to: expected },
          label,
        );
      }
    }
  }
});

test("Accept guard returns the acceptance reasons", () => {
  const reasons = [
    { code: "restaurant_closed" },
    { code: "temporarily_unavailable", menuItemId: "pho" },
  ];
  const outcome = resolveTransition(S.Received, {
    trigger: "Accept",
    eligibility: { satisfied: false, reasons },
  });

  assert.ok(!outcome.ok);
  assert.ok(outcome.error instanceof GuardFailedError);
  assert.deepEqual(outcome.error.reasons, reasons);
});

test("ReportDelay guard requires a time later than the current one and now", () => {
  const cases: Array<{
    current: Date | undefined;
    requested: Date;
    ok: boolean;
  }> = [
    { current: at(10), requested: at(11), ok: true },
    { current: at(10), requested: at(10), ok: false },
    { current: at(10), requested: at(9), ok: false },
    { current: undefined, requested: at(1), ok: true },
    { current: undefined, requested: at(0), ok: false },
    { current: at(-10), requested: at(0), ok: false },
  ];
  for (const { current, requested, ok } of cases) {
    for (const state of [S.Queued, S.Preparing, S.Assembled]) {
      const outcome = resolveTransition(state, {
        trigger: "ReportDelay",
        now: at(0),
        currentExpectedReadyAt: current,
        requestedExpectedReadyAt: requested,
      });
      assert.equal(
        outcome.ok,
        ok,
        `${state} ${current?.toISOString()} → ${requested.toISOString()}`,
      );
      if (!outcome.ok) {
        assert.ok(outcome.error instanceof GuardFailedError);
        assert.deepEqual(outcome.error.reasons, [{ code: "not_later" }]);
      }
    }
  }
});

test("Cancel during Preparation follows the cancellation policy", () => {
  for (const state of [S.Preparing, S.Assembled]) {
    const rejected = resolveTransition(state, {
      trigger: "Cancel",
      policy: { duringPreparation: "reject" },
    });
    assert.ok(!rejected.ok);
    assert.ok(rejected.error instanceof GuardFailedError);
    assert.deepEqual(rejected.error.reasons, [
      { code: "cancellation_during_preparation_not_allowed" },
    ]);
  }
  for (const state of [S.Received, S.Accepted, S.Queued]) {
    assert.equal(
      resolveTransition(state, {
        trigger: "Cancel",
        policy: { duringPreparation: "reject" },
      }).ok,
      true,
      state,
    );
  }
});

test("table declares each node × trigger once and repeats only on leaves", () => {
  const declared = TRANSITIONS.map((row) => `${row.source} × ${row.trigger}`);
  assert.equal(new Set(declared).size, declared.length);
  for (const repeat of REPEATS) {
    assert.ok(ALL_STATES.includes(repeat.state), repeat.state);
    assert.ok(
      !declared.includes(`${repeat.state} × ${repeat.trigger}`),
      `${repeat.state} × ${repeat.trigger}`,
    );
  }
});

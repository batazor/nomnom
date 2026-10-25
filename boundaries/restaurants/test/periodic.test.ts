import assert from "node:assert/strict";
import test from "node:test";
import { setTimeout as delay } from "node:timers/promises";

import type { Worker } from "../pkg/lifecycle/lifecycle.ts";
import { PeriodicLoop, WorkerWithLoops } from "../pkg/lifecycle/periodic.ts";

test("periodic loop runs after the initial delay, repeats and survives failures", async () => {
  let runs = 0;
  const logs: string[] = [];
  const loop = new PeriodicLoop(
    async () => {
      runs += 1;
      if (runs === 1) throw new Error("boom");
    },
    { initialDelayMs: 5, intervalMs: 5, log: (message) => logs.push(message) },
  );

  loop.start();
  await delay(60);
  await loop.stop();
  const stoppedAt = runs;
  await delay(30);

  assert.ok(stoppedAt >= 2, `runs: ${stoppedAt}`);
  assert.equal(runs, stoppedAt);
  assert.match(logs[0] ?? "", /boom/);
});

test("stop aborts the running task and waits for it", async () => {
  let finished = false;
  const loop = new PeriodicLoop(
    async (signal) => {
      await new Promise<void>((resolve) =>
        signal.addEventListener("abort", () => resolve(), { once: true }),
      );
      finished = true;
    },
    { initialDelayMs: 0, intervalMs: 1000 },
  );

  loop.start();
  await delay(10);
  await loop.stop();

  assert.equal(finished, true);
});

test("worker with loops stops the loops before the worker drains", async () => {
  const events: string[] = [];
  let resolveRun: () => void = () => {};
  const worker: Worker = {
    start: async () => {
      events.push("start");
    },
    run: () =>
      new Promise<void>((resolve) => {
        resolveRun = resolve;
      }),
    shutdown: async () => {
      events.push("shutdown");
      resolveRun();
    },
  };
  const loop = new PeriodicLoop(
    async (signal) => {
      events.push("pass");
      await new Promise<void>((resolve) =>
        signal.addEventListener("abort", () => resolve(), { once: true }),
      );
      events.push("pass stopped");
    },
    { initialDelayMs: 0, intervalMs: 1000 },
  );
  const composite = new WorkerWithLoops(worker, [loop]);

  await composite.start();
  const running = composite.run();
  await delay(10);
  await composite.shutdown(AbortSignal.timeout(1000));
  await running;

  assert.deepEqual(events, ["start", "pass", "pass stopped", "shutdown"]);
});

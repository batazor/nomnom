import assert from "node:assert/strict";
import { createServer, request } from "node:http";
import test from "node:test";
import { setTimeout as delay } from "node:timers/promises";

import {
  HttpWorker,
  Lifecycle,
  type Worker,
} from "../pkg/lifecycle/lifecycle.ts";

const config = {
  managementPort: 0,
  readinessDelayMs: 50,
  drainTimeoutMs: 200,
};

class FakeWorker implements Worker {
  startError?: Error;
  drainMs = 0;
  readonly started = Promise.withResolvers<void>();
  readonly #exit = Promise.withResolvers<void>();

  async start(): Promise<void> {
    if (this.startError) throw this.startError;
    this.started.resolve();
  }

  run(): Promise<void> {
    return this.#exit.promise;
  }

  fail(error: Error): void {
    this.#exit.reject(error);
  }

  async shutdown(deadline: AbortSignal): Promise<void> {
    try {
      await delay(this.drainMs, undefined, { signal: deadline });
    } finally {
      this.#exit.resolve();
    }
  }
}

const silent = () => {};

test("marks ready after start and shuts down gracefully", async () => {
  const lifecycle = new Lifecycle(config);
  const worker = new FakeWorker();
  const stop = new AbortController();
  const result = lifecycle.run(worker, { signal: stop.signal, log: silent });

  await worker.started.promise;
  await delay(1);
  assert.equal(lifecycle.health.ready, true);

  stop.abort();
  await delay(1);
  assert.equal(lifecycle.health.ready, false);
  await result;
});

test("waits the readiness delay before shutdown", async () => {
  const lifecycle = new Lifecycle({ ...config, readinessDelayMs: 150 });
  const worker = new FakeWorker();
  const stop = new AbortController();
  const result = lifecycle.run(worker, { signal: stop.signal, log: silent });
  await worker.started.promise;

  const begin = performance.now();
  stop.abort();
  await result;
  assert.ok(performance.now() - begin >= 145);
});

test("fails when the drain timeout is exceeded", async () => {
  const lifecycle = new Lifecycle(config);
  const worker = new FakeWorker();
  worker.drainMs = 60_000;
  const stop = new AbortController();
  const result = lifecycle.run(worker, { signal: stop.signal, log: silent });
  await worker.started.promise;

  stop.abort();
  await assert.rejects(result);
});

test("fails without becoming ready when start fails", async () => {
  const lifecycle = new Lifecycle(config);
  const worker = new FakeWorker();
  worker.startError = new Error("address in use");

  await assert.rejects(
    lifecycle.run(worker, { log: silent }),
    worker.startError,
  );
  assert.equal(lifecycle.health.ready, false);
});

test("fails when the worker stops on its own", async () => {
  const lifecycle = new Lifecycle(config);
  const worker = new FakeWorker();
  const result = lifecycle.run(worker, { log: silent });
  await worker.started.promise;

  worker.fail(new Error("listener closed"));
  await assert.rejects(result, /worker stopped before shutdown/);
  assert.equal(lifecycle.health.ready, false);
});

test("serves health on the management port", async () => {
  const lifecycle = new Lifecycle(config);
  const worker = new FakeWorker();
  const stop = new AbortController();
  const port = Promise.withResolvers<number>();
  const result = lifecycle.run(worker, {
    signal: stop.signal,
    log: silent,
    onManagementListening: port.resolve,
  });
  const managementPort = await port.promise;
  await worker.started.promise;
  await delay(1);

  assert.equal(await status(managementPort, "/health/startup"), 200);
  assert.equal(await status(managementPort, "/health/live"), 200);
  assert.equal(await status(managementPort, "/health/ready"), 200);

  stop.abort();
  await delay(1);
  assert.equal(await status(managementPort, "/health/live"), 200);
  assert.equal(await status(managementPort, "/health/ready"), 503);
  await result;
});

test("http worker waits for in-flight requests within the deadline", async () => {
  const server = createServer(async (_request, response) => {
    await delay(100);
    response.end("done");
  });
  const worker = new HttpWorker(server, 0);
  await worker.start();
  const running = worker.run();

  const inFlight = status(worker.port, "/");
  await delay(20);
  await worker.shutdown(AbortSignal.timeout(1000));
  assert.equal(await inFlight, 200);
  await running;
});

test("http worker closes connections when the deadline expires", async () => {
  const server = createServer(async (_request, response) => {
    await delay(5_000);
    response.end("late");
  });
  const worker = new HttpWorker(server, 0);
  await worker.start();
  const running = worker.run();

  const inFlight = status(worker.port, "/").catch(() => "aborted");
  await delay(20);
  await assert.rejects(worker.shutdown(AbortSignal.timeout(50)));
  assert.equal(await inFlight, "aborted");
  await running;
});

function status(port: number, path: string): Promise<number> {
  return new Promise((resolve, reject) => {
    request({ port, path, agent: false }, (response) => {
      response.resume();
      resolve(response.statusCode ?? 0);
    })
      .on("error", reject)
      .end();
  });
}

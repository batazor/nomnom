import assert from "node:assert/strict";
import test from "node:test";

import { loadConfig } from "../pkg/config/config.ts";

test("uses the default port", () => {
  assert.equal(loadConfig({}).server.port, 8082);
});

test("SERVER__PORT takes precedence over PORT", () => {
  const config = loadConfig({
    SERVER__PORT: "9002",
    PORT: "9001",
  });

  assert.equal(config.server.port, 9002);
});

test("rejects an invalid port", () => {
  assert.throws(() => loadConfig({ PORT: "not-a-port" }));
  assert.throws(() => loadConfig({ PORT: "70000" }));
});

test("uses default management port and shutdown budget", () => {
  const config = loadConfig({});

  assert.equal(config.management.port, 9082);
  assert.deepEqual(config.shutdown, {
    readinessDelayMs: 5000,
    drainTimeoutMs: 20000,
  });
});

test("reads shutdown budget from the environment", () => {
  const config = loadConfig({
    SHUTDOWN__READINESS_DELAY: "0s",
    SHUTDOWN__DRAIN_TIMEOUT: "1500ms",
  });

  assert.deepEqual(config.shutdown, {
    readinessDelayMs: 0,
    drainTimeoutMs: 1500,
  });
});

test("rejects an invalid shutdown budget", () => {
  assert.throws(() => loadConfig({ SHUTDOWN__DRAIN_TIMEOUT: "0s" }));
  assert.throws(() => loadConfig({ SHUTDOWN__READINESS_DELAY: "5" }));
});

test("rejects a management port equal to the server port", () => {
  assert.throws(() => loadConfig({ MANAGEMENT__PORT: "8082" }));
});

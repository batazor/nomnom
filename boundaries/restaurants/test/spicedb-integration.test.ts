import assert from "node:assert/strict";
import { after, describe, test } from "node:test";

import { SpiceDbAccessControl } from "../infrastructure/spicedb/access-control.ts";
import {
  createSpiceDbClient,
  type SpiceDbClient,
} from "../infrastructure/spicedb/client.ts";
import { AuthorizationUnavailableError } from "../pkg/application/errors.ts";
import type { SpiceDbEnabledConfig } from "../pkg/config/config.ts";

// Runs against SpiceDB bootstrapped with authorization/bootstrap.yaml
// (`make spicedb-dev`): SPICEDB_ENDPOINT=localhost:50051, key `devkey`.
const endpoint = process.env.SPICEDB_ENDPOINT?.trim() ?? "";
const skip = endpoint === "" ? "SPICEDB_ENDPOINT is not set" : false;

const clients: SpiceDbClient[] = [];

function accessControl(
  config: Partial<SpiceDbEnabledConfig> = {},
): SpiceDbAccessControl {
  const resolved: SpiceDbEnabledConfig = {
    enabled: true,
    endpoint,
    presharedKey: "devkey",
    insecure: true,
    timeoutMs: 2000,
    ...config,
  };
  const client = createSpiceDbClient(resolved);
  clients.push(client);
  return new SpiceDbAccessControl({
    client,
    revisions: { authorizationRevision: async () => undefined },
    timeoutMs: resolved.timeoutMs,
  });
}

describe("SpiceDB access control", { skip }, () => {
  after(() => {
    for (const client of clients) {
      client.close();
    }
  });

  test("orders-worker coordinates fulfillment", async () => {
    assert.equal(
      await accessControl().canCoordinateFulfillment(
        { type: "service", id: "orders-worker" },
        "restaurant-1",
      ),
      true,
    );
  });

  test("another service does not coordinate fulfillment", async () => {
    assert.equal(
      await accessControl().canCoordinateFulfillment(
        { type: "service", id: "bff" },
        "restaurant-1",
      ),
      false,
    );
  });

  test("orders-worker cannot register restaurants", async () => {
    assert.equal(
      await accessControl().canRegisterRestaurant({
        type: "service",
        id: "orders-worker",
      }),
      false,
    );
  });

  test("unreachable SpiceDB fails closed within the timeout", async () => {
    const started = Date.now();
    await assert.rejects(
      accessControl({
        endpoint: "localhost:1",
        timeoutMs: 300,
      }).canCoordinateFulfillment(
        { type: "service", id: "orders-worker" },
        "restaurant-1",
      ),
      AuthorizationUnavailableError,
    );
    assert.ok(Date.now() - started < 2000);
  });
});

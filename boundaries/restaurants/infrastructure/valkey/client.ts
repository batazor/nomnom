// Valkey client of the catalog read cache (ADR 43, local ADR 3). The cache
// only speeds reads up: the process starts and serves without a reachable
// Valkey, and every command fails fast while the connection is down (no
// offline queue, no per-request retries), so readers go to MongoDB instead.
import { Valkey } from "iovalkey";

import type { ValkeyConfig } from "../../pkg/config/config.ts";
import type { Logger } from "../../pkg/telemetry/logger.ts";

/** Reconnect backoff after a lost connection. */
const RECONNECT_STEP_MS = 200;
const RECONNECT_MAX_MS = 2_000;

export type ValkeyConnection = Readonly<{
  client: Valkey;
  close(): Promise<void>;
}>;

/**
 * Opens the client in the background: startup does not wait for Valkey.
 * Logs only availability changes, not every failed reconnect.
 */
export function connectValkey(
  config: Pick<ValkeyConfig, "url" | "commandTimeoutMs">,
  options: Readonly<{ connectionName: string; logger: Logger }>,
): ValkeyConnection {
  const client = new Valkey(config.url, {
    connectionName: options.connectionName,
    commandTimeout: config.commandTimeoutMs,
    connectTimeout: RECONNECT_MAX_MS,
    enableOfflineQueue: false,
    maxRetriesPerRequest: 0,
    retryStrategy: (times) =>
      Math.min(times * RECONNECT_STEP_MS, RECONNECT_MAX_MS),
  });
  let down = false;
  const markDown = (error?: Error) => {
    if (down) return;
    down = true;
    options.logger.warn("valkey unavailable, reads bypass the cache", {
      ...(error === undefined ? {} : { error }),
    });
  };
  client.on("ready", () => {
    down = false;
    options.logger.info("valkey available");
  });
  client.on("error", (error: Error) => markDown(error));
  client.on("close", () => markDown());
  return Object.freeze({
    client,
    close: async () => {
      if (client.status === "ready") {
        await client.quit();
        return;
      }
      client.disconnect();
    },
  });
}

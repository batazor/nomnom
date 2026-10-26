import { Valkey } from "iovalkey";

import type { ValkeyEnabledConfig } from "../../pkg/config/config.ts";

/** Config of the read cache in tests; the URL decides which Valkey it reaches. */
export const testValkeyConfig = (url: string): ValkeyEnabledConfig => ({
  enabled: true,
  url,
  commandTimeoutMs: 200,
  ttlMs: 60_000,
});

/** A client that is never connected: building the graph opens no socket. */
export const offlineValkey = (): Valkey =>
  new Valkey("redis://127.0.0.1:1", { lazyConnect: true });

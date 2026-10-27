// Refresh of the read cache by Integration Events of the context itself
// (ADR 43, local ADR 3): Debezium publishes them from the Outbox right after
// the commit, restaurants-consumer reads them in its own consumer group and
// rewrites the changed keys from MongoDB. A refresh reads the current
// document, so redeliveries and reordering within the TTL need no Inbox.
// While Valkey is unavailable the message is retried with backoff and its
// offset is not committed: once Valkey is back, every refresh is applied.
import { setTimeout as delay } from "node:timers/promises";

import type { MessageHandler } from "../kafka/consumer.ts";
import { type InboundMessage, parseEnvelope } from "../messaging/envelope.ts";
import type { ProcessOutcome, RetryPolicy } from "../messaging/processor.ts";

/** Refresh of the cache by event_type: payload → keys rewritten from the source. */
export type CacheRefreshes = Readonly<
  Record<string, (payload: Uint8Array) => Promise<void>>
>;

export class CacheRefreshHandler implements MessageHandler {
  readonly #refreshes: CacheRefreshes;
  readonly #retry: Pick<RetryPolicy, "initialIntervalMs" | "maxIntervalMs">;
  readonly #warn: (message: string, fields?: Record<string, unknown>) => void;

  constructor(options: {
    refreshes: CacheRefreshes;
    retry: Pick<RetryPolicy, "initialIntervalMs" | "maxIntervalMs">;
    warn: (message: string, fields?: Record<string, unknown>) => void;
  }) {
    this.#refreshes = options.refreshes;
    this.#retry = options.retry;
    this.#warn = options.warn;
  }

  async process(
    message: InboundMessage,
    signal: AbortSignal,
  ): Promise<ProcessOutcome> {
    let eventType: string;
    try {
      eventType = parseEnvelope(message.headers).eventType;
    } catch (error) {
      this.#warn("malformed event, cache not refreshed", {
        error,
        "messaging.destination.name": message.topic,
        "messaging.kafka.offset": message.offset,
      });
      return "rejected";
    }
    const refresh = this.#refreshes[eventType];
    if (refresh === undefined) return "ignored";
    let intervalMs = this.#retry.initialIntervalMs;
    for (;;) {
      try {
        await refresh(message.value);
        return "processed";
      } catch (error) {
        if (signal.aborted) throw error;
        this.#warn("cache refresh failed, retrying", {
          error,
          "nomnom.event.type": eventType,
        });
        // Rejects on abort: the offset stays uncommitted for redelivery.
        await delay(intervalMs, undefined, { signal });
        intervalMs = Math.min(intervalMs * 2, this.#retry.maxIntervalMs);
      }
    }
  }
}

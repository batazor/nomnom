// Cache-aside over Valkey with TTL only (ADR 40, local ADR 3): the catalog
// changes rarely, so an entry lives `ttlMs` and is not invalidated on writes;
// a change becomes visible at most `ttlMs` later. Reads never fail on Valkey:
// a disconnected client, a timeout or an error reads from the source
// (bypass).
import { type Meter, metrics } from "@opentelemetry/api";
import type { Valkey } from "iovalkey";

export type CacheLookupResult = "hit" | "miss" | "bypass";
export type CacheOperation = "get" | "set";

/** Metrics of docs/observability/metric-inventory.md#cache, without keys. */
export class CacheMetrics {
  readonly #lookups;
  readonly #errors;

  constructor(meter: Meter = metrics.getMeter("restaurants")) {
    this.#lookups = meter.createCounter("nomnom.cache.lookups", {
      description:
        "Cached reads by result: hit, miss (read from the source and stored) or bypass (Valkey unavailable).",
      unit: "{lookup}",
    });
    this.#errors = meter.createCounter("nomnom.cache.errors", {
      description: "Failed Valkey commands of the cache by operation.",
      unit: "{error}",
    });
  }

  lookup(cache: string, result: CacheLookupResult): void {
    this.#lookups.add(1, {
      "nomnom.cache.name": cache,
      "nomnom.cache.result": result,
    });
  }

  error(cache: string, operation: CacheOperation): void {
    this.#errors.add(1, {
      "nomnom.cache.name": cache,
      "nomnom.cache.operation": operation,
    });
  }
}

export class ReadCache<T> {
  readonly #client: Valkey;
  readonly #name: string;
  readonly #prefix: string;
  readonly #ttlMs: number;
  readonly #metrics: CacheMetrics;

  constructor(options: {
    client: Valkey;
    /** `nomnom.cache.name` of the metrics, e.g. `menu`. */
    name: string;
    /** Key prefix within the ACL key pattern of the context, e.g. `restaurants:menu:`. */
    prefix: string;
    ttlMs: number;
    metrics: CacheMetrics;
  }) {
    this.#client = options.client;
    this.#name = options.name;
    this.#prefix = options.prefix;
    this.#ttlMs = options.ttlMs;
    this.#metrics = options.metrics;
  }

  /**
   * The cached value, or the value of `load` stored for `ttlMs`. A missing
   * source value is not cached. Valkey failures fall through to `load`.
   */
  async read<R extends T | undefined>(
    key: string,
    load: () => Promise<R>,
  ): Promise<T | R> {
    const fullKey = this.#prefix + key;
    if (this.#client.status !== "ready") {
      this.#metrics.lookup(this.#name, "bypass");
      return load();
    }
    let cached: string | null;
    try {
      cached = await this.#client.get(fullKey);
    } catch {
      this.#metrics.error(this.#name, "get");
      this.#metrics.lookup(this.#name, "bypass");
      return load();
    }
    if (cached !== null) {
      this.#metrics.lookup(this.#name, "hit");
      return JSON.parse(cached) as T;
    }
    this.#metrics.lookup(this.#name, "miss");
    const loaded = await load();
    if (loaded !== undefined) {
      // Not awaited: the reply does not wait for the write.
      this.#client
        .set(fullKey, JSON.stringify(loaded), "PX", this.#ttlMs)
        .catch(() => this.#metrics.error(this.#name, "set"));
    }
    return loaded;
  }
}

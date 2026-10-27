// Cache-aside over Valkey (ADR 43, local ADR 3). A miss stores the source
// value with SET NX for `ttlMs`; an Integration Event of the change refreshes
// the key with plain SET from the source read after the commit. A read that
// started before the change cannot put the old value back: its NX store
// loses to the refresh, or the refresh overwrites it. Changes without an
// event become visible within the TTL. Reads never fail on Valkey: a
// disconnected client, a timeout or an error reads from the source (bypass).
import { type Meter, metrics } from "@opentelemetry/api";
import type { Valkey } from "iovalkey";

export type CacheLookupResult = "hit" | "miss" | "bypass";
export type CacheOperation = "get" | "set" | "refresh";

/** Metrics of docs/observability/metric-inventory.md#cache, without keys. */
export class CacheMetrics {
  readonly #lookups;
  readonly #errors;
  readonly #refreshes;

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
    this.#refreshes = meter.createCounter("nomnom.cache.refreshes", {
      description:
        "Keys refreshed or generations started by Integration Events of the change.",
      unit: "{refresh}",
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

  refreshed(cache: string): void {
    this.#refreshes.add(1, { "nomnom.cache.name": cache });
  }
}

export class ReadCache<T> {
  readonly #client: Valkey;
  readonly #name: string;
  readonly #prefix: string;
  readonly #ttlMs: number;
  readonly #metrics: CacheMetrics;
  readonly #isCurrent: ((value: T) => boolean) | undefined;

  constructor(options: {
    client: Valkey;
    /** `nomnom.cache.name` of the metrics, e.g. `menu`. */
    name: string;
    /** Key prefix within the ACL key pattern of the context, e.g. `restaurants:menu:`. */
    prefix: string;
    ttlMs: number;
    metrics: CacheMetrics;
    /**
     * Whether a cached value has the current shape. A value that does not
     * (written by an older release) is a miss: it is deleted and replaced by
     * the source value.
     */
    isCurrent?: (value: T) => boolean;
  }) {
    this.#client = options.client;
    this.#name = options.name;
    this.#prefix = options.prefix;
    this.#ttlMs = options.ttlMs;
    this.#metrics = options.metrics;
    this.#isCurrent = options.isCurrent;
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
    if (!this.#available) {
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
      const value = JSON.parse(cached) as T;
      if (this.#isCurrent === undefined || this.#isCurrent(value)) {
        this.#metrics.lookup(this.#name, "hit");
        return value;
      }
      // Outdated shape: drop it so the NX store below replaces it; a refresh
      // that writes in between still wins.
      try {
        await this.#client.del(fullKey);
      } catch {
        this.#metrics.error(this.#name, "set");
        this.#metrics.lookup(this.#name, "bypass");
        return load();
      }
    }
    this.#metrics.lookup(this.#name, "miss");
    const loaded = await load();
    if (loaded !== undefined) {
      // Not awaited: the reply does not wait for the write. NX: a refresh
      // that already wrote a newer value wins.
      this.#client
        .set(fullKey, JSON.stringify(loaded), "PX", this.#ttlMs, "NX")
        .catch(() => this.#metrics.error(this.#name, "set"));
    }
    return loaded;
  }

  /**
   * Overwrites the key with the current source value, or removes it when the
   * source has none. A Valkey failure is thrown to the caller.
   */
  async refresh(key: string, value: T | undefined): Promise<void> {
    const fullKey = this.#prefix + key;
    try {
      if (value === undefined) {
        await this.#client.del(fullKey);
      } else {
        await this.#client.set(
          fullKey,
          JSON.stringify(value),
          "PX",
          this.#ttlMs,
        );
      }
    } catch (error) {
      this.#metrics.error(this.#name, "refresh");
      throw error;
    }
    this.#metrics.refreshed(this.#name);
  }

  /**
   * Read within the current generation of `scope`: entries that cannot be
   * refreshed one by one (pages of a list) are dropped together by
   * {@link nextGeneration}. A store of an old generation is never read again.
   */
  async readInGeneration<R extends T | undefined>(
    scope: string,
    key: string,
    load: () => Promise<R>,
  ): Promise<T | R> {
    if (!this.#available) {
      this.#metrics.lookup(this.#name, "bypass");
      return load();
    }
    let generation: string | null;
    try {
      generation = await this.#client.get(this.#generationKey(scope));
    } catch {
      this.#metrics.error(this.#name, "get");
      this.#metrics.lookup(this.#name, "bypass");
      return load();
    }
    // INCR counts an absent generation as 0, so does the reader.
    return this.read(`${generation === null ? "0" : generation}:${key}`, load);
  }

  /** Starts a new generation of `scope`; a Valkey failure is thrown. */
  async nextGeneration(scope: string): Promise<void> {
    try {
      await this.#client.incr(this.#generationKey(scope));
    } catch (error) {
      this.#metrics.error(this.#name, "refresh");
      throw error;
    }
    this.#metrics.refreshed(this.#name);
  }

  get #available(): boolean {
    return this.#client.status === "ready";
  }

  #generationKey(scope: string): string {
    return `${this.#prefix}generation:${scope}`;
  }
}

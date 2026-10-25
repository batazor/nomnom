import { type Meter, metrics } from "@opentelemetry/api";

import type { PendingStats, RelayMetrics, RelayRecord } from "./relay.ts";

const PUBLISH_DURATION_BUCKETS = [
  0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
];
const LAG_BUCKETS = [
  0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 300, 900, 3600,
];

/**
 * Outbox metrics of the relay (docs/observability/metric-inventory.md#outbox).
 * Labels: `stream` (topic), `event_type`, `boundary`, `error_class`; never
 * event or aggregate ids.
 */
export class OtelRelayMetrics implements RelayMetrics {
  readonly #boundary: string;
  #backlog: readonly PendingStats[] = [];
  readonly #retries;
  readonly #duration;
  readonly #lag;

  constructor(boundary: string, meter: Meter = metrics.getMeter(boundary)) {
    this.#boundary = boundary;
    this.#retries = meter.createCounter("nomnom_outbox_publish_retries_total", {
      description:
        "Failed Outbox publish attempts; the record is retried next cycle.",
    });
    this.#duration = meter.createHistogram(
      "nomnom_outbox_publish_duration_seconds",
      {
        description: "Kafka publish of one Outbox record until broker ack.",
        unit: "s",
        advice: { explicitBucketBoundaries: PUBLISH_DURATION_BUCKETS },
      },
    );
    this.#lag = meter.createHistogram("nomnom_outbox_publish_lag_seconds", {
      description: "Delay from occurred_at to the broker ack.",
      unit: "s",
      advice: { explicitBucketBoundaries: LAG_BUCKETS },
    });
    const pending = meter.createObservableGauge(
      "nomnom_outbox_pending_messages",
      { description: "Unpublished Outbox records." },
    );
    const oldest = meter.createObservableGauge(
      "nomnom_outbox_oldest_pending_age_seconds",
      {
        description: "Age of the oldest unpublished Outbox record.",
        unit: "s",
      },
    );
    meter.addBatchObservableCallback(
      (result) => {
        const now = Date.now();
        for (const stats of this.#backlog) {
          const attributes = { stream: stats.stream, boundary: this.#boundary };
          result.observe(pending, stats.pending, attributes);
          result.observe(
            oldest,
            stats.oldestOccurredAt === undefined
              ? 0
              : Math.max(0, now - stats.oldestOccurredAt.getTime()) / 1000,
            attributes,
          );
        }
      },
      [pending, oldest],
    );
  }

  published(record: RelayRecord, durationS: number, lagS: number): void {
    const attributes = this.#attributes(record);
    this.#duration.record(durationS, attributes);
    this.#lag.record(lagS, attributes);
  }

  failed(record: RelayRecord, errorClass: string): void {
    this.#retries.add(1, {
      ...this.#attributes(record),
      error_class: errorClass,
    });
  }

  backlog(stats: readonly PendingStats[]): void {
    this.#backlog = stats;
  }

  #attributes(record: RelayRecord) {
    return {
      stream: record.topic,
      event_type: record.eventType,
      boundary: this.#boundary,
    };
  }
}

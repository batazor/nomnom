import { type Meter, metrics } from "@opentelemetry/api";

import type { ConsumerMetrics, MessageLabels } from "./processor.ts";

const DURATION_BUCKETS = [
  0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30,
];
const AGE_BUCKETS = [
  0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 300, 900, 3600,
];

/**
 * Метрики consumer по docs/observability/metric-inventory.md, те же имена,
 * что у orders-consumer. Labels: `handler_name`, `event_type`, `stream`,
 * `error_class`; без event и aggregate id.
 */
export class OtelConsumerMetrics implements ConsumerMetrics {
  readonly #duration;
  readonly #retries;
  readonly #poison;
  readonly #duplicates;
  readonly #age;

  constructor(meter: Meter = metrics.getMeter("restaurants-consumer")) {
    this.#duration = meter.createHistogram(
      "nomnom_messaging_process_duration_seconds",
      {
        description:
          "Processing of one inbound message, including retries; error_class only on failure.",
        unit: "s",
        advice: { explicitBucketBoundaries: DURATION_BUCKETS },
      },
    );
    this.#retries = meter.createCounter(
      "nomnom_messaging_retry_attempts_total",
      { description: "In-process retries of transient failures." },
    );
    this.#poison = meter.createCounter(
      "nomnom_messaging_poison_messages_total",
      {
        description: "Messages sent to <stream>.dlq.",
      },
    );
    this.#duplicates = meter.createCounter("nomnom_inbox_duplicates_total", {
      description:
        "Messages whose (consumer, event_id) is already in the Inbox.",
    });
    this.#age = meter.createHistogram("nomnom_messaging_message_age_seconds", {
      description: "now - occurred_at when processing starts.",
      unit: "s",
      advice: { explicitBucketBoundaries: AGE_BUCKETS },
    });
  }

  processed(labels: MessageLabels, durationS: number, errorClass?: string) {
    this.#duration.record(durationS, {
      ...attributes(labels),
      ...(errorClass === undefined ? {} : { error_class: errorClass }),
    });
  }

  retried(labels: MessageLabels) {
    this.#retries.add(1, attributes(labels));
  }

  poisoned(labels: MessageLabels, errorClass: string) {
    this.#poison.add(1, { ...attributes(labels), error_class: errorClass });
  }

  duplicate(labels: MessageLabels) {
    this.#duplicates.add(1, attributes(labels));
  }

  messageAge(labels: MessageLabels, ageS: number) {
    this.#age.record(ageS, attributes(labels));
  }
}

const attributes = (labels: MessageLabels) => ({
  handler_name: labels.handlerName,
  event_type: labels.eventType,
  stream: labels.stream,
});

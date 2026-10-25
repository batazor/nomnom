import { type Meter, metrics } from "@opentelemetry/api";

import type { ConsumerMetrics, MessageLabels } from "./processor.ts";

const DURATION_BUCKETS = [
  0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30,
];
const AGE_BUCKETS = [
  0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 300, 900, 3600,
];

// Attributes из registry ops/semconv (make semconv-check), те же, что у orders-consumer.
const HANDLER = "nomnom.messaging.handler";
const EVENT_TYPE = "nomnom.event.type";
const DESTINATION = "messaging.destination.name";
const ERROR_CLASS = "nomnom.error.class";

/**
 * Метрики consumer по docs/observability/metric-inventory.md: OTel-имена registry
 * ops/semconv, в Mimir — те же, что у orders-consumer; без event и aggregate id.
 */
export class OtelConsumerMetrics implements ConsumerMetrics {
  readonly #duration;
  readonly #retries;
  readonly #poison;
  readonly #duplicates;
  readonly #age;

  constructor(meter: Meter = metrics.getMeter("restaurants-consumer")) {
    this.#duration = meter.createHistogram(
      "nomnom.messaging.process.duration",
      {
        description:
          "Processing of one inbound message, including retries; error_class only on failure.",
        unit: "s",
        advice: { explicitBucketBoundaries: DURATION_BUCKETS },
      },
    );
    this.#retries = meter.createCounter("nomnom.messaging.retry.attempts", {
      description: "In-process retries of transient failures.",
      unit: "{attempt}",
    });
    this.#poison = meter.createCounter("nomnom.messaging.poison.messages", {
      description: "Messages sent to <stream>.dlq.",
      unit: "{message}",
    });
    this.#duplicates = meter.createCounter("nomnom.inbox.duplicates", {
      unit: "{message}",
      description:
        "Messages whose (consumer, event_id) is already in the Inbox.",
    });
    this.#age = meter.createHistogram("nomnom.messaging.message.age", {
      description: "now - occurred_at when processing starts.",
      unit: "s",
      advice: { explicitBucketBoundaries: AGE_BUCKETS },
    });
  }

  processed(labels: MessageLabels, durationS: number, errorClass?: string) {
    this.#duration.record(durationS, {
      ...attributes(labels),
      ...(errorClass === undefined ? {} : { [ERROR_CLASS]: errorClass }),
    });
  }

  retried(labels: MessageLabels) {
    this.#retries.add(1, attributes(labels));
  }

  poisoned(labels: MessageLabels, errorClass: string) {
    this.#poison.add(1, { ...attributes(labels), [ERROR_CLASS]: errorClass });
  }

  duplicate(labels: MessageLabels) {
    this.#duplicates.add(1, attributes(labels));
  }

  messageAge(labels: MessageLabels, ageS: number) {
    this.#age.record(ageS, attributes(labels));
  }
}

const attributes = (labels: MessageLabels) => ({
  [HANDLER]: labels.handlerName,
  [EVENT_TYPE]: labels.eventType,
  [DESTINATION]: labels.stream,
});

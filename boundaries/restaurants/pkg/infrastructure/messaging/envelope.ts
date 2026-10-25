// Event Envelope входящего Integration Event (ADR 25, ADR 28): Kafka headers
// того же формата, что публикуют orders (pkg/messaging/envelope.go) и
// restaurants-relay (pkg/infrastructure/outbox/relay.ts). Payload — Protobuf
// контракта; event_type — полное имя сообщения.

/** Kafka message, прочитанное consumer'ом, без зависимостей от клиента Kafka. */
export type InboundMessage = Readonly<{
  topic: string;
  partition: number;
  offset: string;
  key: Uint8Array | undefined;
  value: Uint8Array;
  headers: Readonly<Record<string, string>>;
}>;

export type Envelope = Readonly<{
  eventId: string;
  eventType: string;
  occurredAt: Date;
  aggregateType: string;
  aggregateId: string;
  /** 0, если publisher не передал версию. */
  aggregateVersion: number;
  traceparent?: string;
}>;

/**
 * Сообщение нельзя обработать никаким повтором: битый envelope или payload,
 * нарушение контракта. Consumer отправляет его в `<stream>.dlq` и подтверждает
 * offset.
 */
export class PermanentMessageError extends Error {
  readonly code = "permanent_message";
  /** `deserialization` — envelope или payload не разбираются, иначе `permanent`. */
  readonly errorClass: "deserialization" | "permanent";

  constructor(
    message: string,
    errorClass: "deserialization" | "permanent" = "permanent",
    options?: ErrorOptions,
  ) {
    super(message, options);
    this.name = "PermanentMessageError";
    this.errorClass = errorClass;
  }
}

const required = (headers: Readonly<Record<string, string>>, name: string) => {
  const value = headers[name]?.trim() ?? "";
  if (value === "") {
    throw new PermanentMessageError(
      `envelope ${name} is required`,
      "deserialization",
    );
  }
  return value;
};

/** Читает и проверяет envelope; ошибки permanent: повтор их не исправит. */
export function parseEnvelope(
  headers: Readonly<Record<string, string>>,
): Envelope {
  const eventId = required(headers, "event_id");
  const eventType = required(headers, "event_type");
  const rawOccurredAt = required(headers, "occurred_at");
  const aggregateId = required(headers, "aggregate_id");
  const occurredAt = new Date(rawOccurredAt);
  if (Number.isNaN(occurredAt.getTime())) {
    throw new PermanentMessageError(
      `envelope occurred_at "${rawOccurredAt}" is not RFC 3339`,
      "deserialization",
    );
  }
  const rawVersion = headers.aggregate_version?.trim() ?? "";
  const aggregateVersion = rawVersion === "" ? 0 : Number(rawVersion);
  if (!Number.isSafeInteger(aggregateVersion) || aggregateVersion < 0) {
    throw new PermanentMessageError(
      `envelope aggregate_version "${rawVersion}" is not a non-negative integer`,
      "deserialization",
    );
  }
  const traceparent = headers.traceparent?.trim();
  return Object.freeze({
    eventId,
    eventType,
    occurredAt,
    aggregateType: headers.aggregate_type?.trim() ?? "",
    aggregateId,
    aggregateVersion,
    ...(traceparent ? { traceparent } : {}),
  });
}

"""Kafka side of the payments Outbox relay: idempotent producer with delivery reports."""

import logging
import time
from typing import Any

from .outbox import OutboxRecord, PublishError, kafka_headers

logger = logging.getLogger(__name__)

# Upper bound of one delivery report wait; librdkafka retries inside it.
DEFAULT_DELIVERY_TIMEOUT = 30.0


class KafkaPublisher:
    """Idempotent ``confluent-kafka`` producer; one record in flight per call."""

    def __init__(
        self,
        brokers: str,
        client_id: str,
        delivery_timeout: float = DEFAULT_DELIVERY_TIMEOUT,
        extra_config: dict[str, Any] | None = None,
    ) -> None:
        from confluent_kafka import Producer

        self._delivery_timeout = delivery_timeout
        config: dict[str, Any] = {
            "bootstrap.servers": brokers,
            "client.id": client_id,
            "enable.idempotence": True,
            "acks": "all",
            # The delivery report arrives (success or error) before the wait bound.
            "delivery.timeout.ms": int(delivery_timeout * 1000),
        }
        config.update(extra_config or {})
        self._producer = Producer(config)

    def publish(self, record: OutboxRecord) -> None:
        """Produce ``record`` keyed by aggregate ID and wait for its delivery report.

        Raises:
            PublishError: produce failed, the report timed out or reported an error;
                ``retriable`` follows the broker error.
        """
        from confluent_kafka import KafkaException

        report: dict[str, Any] = {}

        def on_delivery(error: Any, _message: Any) -> None:
            report["error"] = error
            report["done"] = True

        try:
            self._producer.produce(
                record.topic,
                key=record.aggregate_id.encode(),
                value=record.payload,
                headers=kafka_headers(record),
                on_delivery=on_delivery,
            )
        except (KafkaException, BufferError) as error:
            raise PublishError(f"produce: {error}") from error
        deadline = time.monotonic() + self._delivery_timeout + 5.0
        while not report.get("done"):
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise PublishError("delivery report timed out")
            self._producer.flush(min(remaining, 1.0))
        error = report.get("error")
        if error is not None:
            raise PublishError(f"delivery: {error}", retriable=bool(error.retriable()))

    def close(self, timeout: float) -> None:
        """Flush queued messages within ``timeout`` seconds; leftovers are logged, not raised."""
        remaining = self._producer.flush(max(timeout, 0.0))
        if remaining:
            logger.warning("kafka producer closed with %d undelivered messages", remaining)

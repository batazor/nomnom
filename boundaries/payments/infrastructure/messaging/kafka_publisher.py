"""Kafka side of the payments Outbox relay: idempotent producer with delivery reports."""

import logging
import time
from collections.abc import Callable
from typing import Any

from .outbox import (
    FatalPublishError,
    OutboxRecord,
    PublishError,
    PublishInterruptedError,
    kafka_headers,
)

logger = logging.getLogger(__name__)

# Upper bound of one delivery report wait; librdkafka retries inside it.
DEFAULT_DELIVERY_TIMEOUT = 30.0

# Slice of one flush while waiting for a delivery report: bounds how late a shutdown
# request or a fatal producer error is noticed.
WAIT_SLICE = 0.1


def _never() -> bool:
    return False


class KafkaPublisher:
    """Idempotent ``confluent-kafka`` producer; one record in flight per call.

    A fatal librdkafka error (``KafkaError.fatal()``, e.g. an idempotent producer
    sequence/PID error) makes the producer unusable: from then on every publish raises
    ``FatalPublishError``.
    """

    def __init__(
        self,
        brokers: str,
        client_id: str,
        delivery_timeout: float = DEFAULT_DELIVERY_TIMEOUT,
        extra_config: dict[str, Any] | None = None,
    ) -> None:
        from confluent_kafka import Producer

        self._delivery_timeout = delivery_timeout
        self._fatal: str | None = None
        config: dict[str, Any] = {
            "bootstrap.servers": brokers,
            "client.id": client_id,
            "enable.idempotence": True,
            "acks": "all",
            # The delivery report arrives (success or error) before the wait bound.
            "delivery.timeout.ms": int(delivery_timeout * 1000),
            "error_cb": self._on_error,
        }
        config.update(extra_config or {})
        self._producer = Producer(config)

    def publish(self, record: OutboxRecord, should_stop: Callable[[], bool] = _never) -> None:
        """Produce ``record`` keyed by aggregate ID and wait for its delivery report.

        The wait is sliced so ``should_stop`` and fatal producer errors are checked
        every ``WAIT_SLICE`` seconds.

        Raises:
            FatalPublishError: the producer hit a fatal error and cannot publish anymore.
            PublishError: produce failed, the report timed out or reported an error;
                ``retriable`` follows the broker error.
            PublishInterruptedError: ``should_stop`` turned true before the report; the
                message may still be delivered while the producer is closed.
        """
        from confluent_kafka import KafkaException

        self._raise_if_fatal()
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
        except KafkaException as error:
            if _is_fatal(error.args[0] if error.args else None):
                self._fatal = self._fatal or str(error)
                raise FatalPublishError(f"produce: {error}") from error
            raise PublishError(f"produce: {error}") from error
        except BufferError as error:
            raise PublishError(f"produce: {error}") from error
        deadline = time.monotonic() + self._delivery_timeout + 5.0
        while not report.get("done"):
            # error_cb runs inside flush: a fatal error ends the wait at once.
            self._raise_if_fatal()
            if should_stop():
                raise PublishInterruptedError("delivery report wait interrupted by shutdown")
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise PublishError("delivery report timed out")
            self._producer.flush(min(remaining, WAIT_SLICE))
        error = report.get("error")
        if error is None:
            return
        if _is_fatal(error):
            self._fatal = self._fatal or str(error)
            raise FatalPublishError(f"delivery: {error}")
        raise PublishError(f"delivery: {error}", retriable=bool(error.retriable()))

    def close(self, timeout: float) -> None:
        """Flush queued messages within ``timeout`` seconds, then purge the leftovers.

        Purging before the caller releases the relay lock keeps a message abandoned on
        shutdown from reaching the broker after the next active replica started.
        Leftovers are logged, not raised.
        """
        remaining = self._producer.flush(max(timeout, 0.0))
        if remaining:
            logger.warning("kafka producer closed with %d undelivered messages", remaining)
            self._producer.purge()
            self._producer.flush(0)

    def _on_error(self, error: Any) -> None:
        # Called by librdkafka from flush/poll on the relay thread.
        if _is_fatal(error):
            self._fatal = self._fatal or str(error)
            logger.error("kafka producer fatal error: %s", error)
        else:
            # librdkafka logs these itself; the delivery report carries what matters.
            logger.debug("kafka producer error: %s", error)

    def _raise_if_fatal(self) -> None:
        if self._fatal is not None:
            raise FatalPublishError(f"kafka producer fatal error: {self._fatal}")


def _is_fatal(error: Any) -> bool:
    """``KafkaError.fatal()`` or the ``_FATAL`` code librdkafka reports after a fatal error."""
    if error is None:
        return False
    from confluent_kafka import KafkaError

    fatal = getattr(error, "fatal", None)
    if callable(fatal) and fatal():
        return True
    code = getattr(error, "code", None)
    return callable(code) and code() == KafkaError._FATAL

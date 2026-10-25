"""OTel instruments of the payments Outbox relay (observability of ADR 13)."""

import threading
from collections.abc import Sequence
from datetime import datetime, timezone
from typing import Callable, Optional

from opentelemetry import metrics

from .outbox import OutboxRecord, TopicBacklog


class RelayMetrics:
    """OTel instruments of the relay (docs/observability/metric-inventory.md#outbox).

    Prometheus names: ``nomnom_outbox_pending_messages``,
    ``nomnom_outbox_oldest_pending_age_seconds``, ``nomnom_outbox_publish_retries_total``,
    ``nomnom_outbox_publish_duration_seconds``, ``nomnom_outbox_publish_lag_seconds``.
    """

    def __init__(
        self,
        meter: Optional[metrics.Meter] = None,
        now: Callable[[], datetime] = lambda: datetime.now(timezone.utc),
    ) -> None:
        meter = meter or metrics.get_meter("nomnom.payments.outbox")
        self._now = now
        self._lock = threading.Lock()
        self._backlog: list[TopicBacklog] = []
        meter.create_observable_gauge(
            "nomnom.outbox.pending.messages",
            callbacks=[self._observe_pending],
            unit="{message}",
            description="Unpublished Outbox records.",
        )
        meter.create_observable_gauge(
            "nomnom.outbox.oldest_pending.age",
            callbacks=[self._observe_oldest_age],
            unit="s",
            description="Age of the oldest unpublished Outbox record.",
        )
        self._retries = meter.create_counter(
            "nomnom.outbox.publish.retries",
            unit="{retry}",
            description="Failed publish attempts of Outbox records.",
        )
        self._duration = meter.create_histogram(
            "nomnom.outbox.publish.duration",
            unit="s",
            description="Produce until broker delivery report of one Outbox record.",
        )
        self._lag = meter.create_histogram(
            "nomnom.outbox.publish.lag",
            unit="s",
            description="occurred_at until broker confirmation of an Outbox record.",
        )

    def set_backlog(self, backlog: Sequence[TopicBacklog]) -> None:
        with self._lock:
            self._backlog = list(backlog)

    def backlog(self) -> list[TopicBacklog]:
        with self._lock:
            return list(self._backlog)

    def published(self, record: OutboxRecord, duration: float, confirmed_at: datetime) -> None:
        attributes = {"stream": record.topic, "event_type": record.event_type}
        self._duration.record(duration, attributes)
        self._lag.record(max(0.0, (confirmed_at - record.occurred_at).total_seconds()), attributes)

    def failed(self, record: OutboxRecord, error_class: str) -> None:
        self._retries.add(1, {"stream": record.topic, "error_class": error_class})

    def _observe_pending(self, _options: metrics.CallbackOptions) -> list[metrics.Observation]:
        return [metrics.Observation(b.pending, {"stream": b.topic}) for b in self.backlog()]

    def _observe_oldest_age(self, _options: metrics.CallbackOptions) -> list[metrics.Observation]:
        now = self._now()
        return [
            metrics.Observation(
                max(0.0, (now - b.oldest_occurred_at).total_seconds()), {"stream": b.topic}
            )
            for b in self.backlog()
            if b.oldest_occurred_at is not None
        ]

"""Events applied on loading a Payment (``event_store`` section of the ops/grafana inventory).

Shows whether snapshots work: without them the histogram follows the stream length, with
them the tail after the snapshot. A read outside a Unit of Work counts too, so it does not
wait for a commit. ``nomnom.event_store.from_snapshot`` is the only attribute: ``payment_id``
stays out of labels (ADR 9).
"""

from opentelemetry import metrics

INSTRUMENTATION = "nomnom.payments.event_store"

_FROM_SNAPSHOT = "nomnom.event_store.from_snapshot"

# Поток обычного заказа — 7-10 событий; границы бакетов различают хвост и весь поток.
REPLAYED_EVENTS_BUCKETS = (0, 1, 2, 3, 5, 8, 13, 21, 34, 55)


class ReplayMetrics:
    """``nomnom.payments.event_store.replayed_events``.

    Prometheus name: ``nomnom_payments_event_store_replayed_events``. The instrument is
    created on the global MeterProvider by default: no-op until telemetry setup.
    """

    def __init__(self, meter: metrics.Meter | None = None) -> None:
        meter = meter or metrics.get_meter(INSTRUMENTATION)
        self._replayed = meter.create_histogram(
            "nomnom.payments.event_store.replayed_events",
            unit="{event}",
            description="Stream events applied on loading a Payment.",
            explicit_bucket_boundaries_advisory=REPLAYED_EVENTS_BUCKETS,
        )

    def loaded(self, replayed_events: int, *, from_snapshot: bool) -> None:
        """Record one load of a Payment."""
        self._replayed.record(replayed_events, {_FROM_SNAPSHOT: from_snapshot})

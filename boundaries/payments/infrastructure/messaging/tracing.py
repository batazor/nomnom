"""Producer span of one Outbox record (semconv messaging, Kafka).

The span ``send <topic>`` (kind ``PRODUCER``) is a child of the trace context stored with
the record when the event was written, so the publish shows up in the trace of the use
case that produced the event. Kafka headers carry that stored context unchanged
(``kafka_headers``): consumers continue or link to the creation context, not to the
relay. The span covers produce until the delivery report; it never stays open while the
message waits in Kafka. Payload and key are not recorded.
"""

from collections.abc import Generator
from contextlib import contextmanager

from opentelemetry import trace
from opentelemetry.trace import Span, SpanKind, Status, StatusCode

from pkg.telemetry import extract_trace_context

from .outbox import OutboxRecord

INSTRUMENTATION = "nomnom.payments.outbox"


@contextmanager
def publish_span(record: OutboxRecord, tracer: trace.Tracer) -> Generator[Span]:
    """Span of publishing ``record``; an exception marks it failed and propagates."""
    with tracer.start_as_current_span(
        f"send {record.topic}",
        context=extract_trace_context(record.metadata),
        kind=SpanKind.PRODUCER,
        attributes={
            "messaging.system": "kafka",
            "messaging.operation.name": "send",
            "messaging.operation.type": "send",
            "messaging.destination.name": record.topic,
            "messaging.message.id": record.event_id,
            "nomnom.event.type": record.event_type,
        },
        record_exception=False,
        set_status_on_exception=False,
    ) as span:
        try:
            yield span
        except BaseException as error:
            span.set_attribute("error.type", type(error).__name__)
            span.set_status(Status(StatusCode.ERROR, type(error).__name__))
            raise

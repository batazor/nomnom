"""PostgreSQL adapters of the payment repository, Outbox and provider message Inbox.

A write runs in a savepoint so that a mapped unique violation rolls its events back and
leaves the surrounding Unit of Work usable.
"""

from collections.abc import Sequence
from datetime import timedelta

import psycopg
from psycopg import errors

from infrastructure.repository.database import Database
from infrastructure.repository.outbox import OutboxEventMapper, outbox_event_id, outbox_traceparent
from pkg.outbox import AggregateRef
from pkg.usecase import ConcurrencyConflictError
from slices.payment.domains.payment import (
    DuplicateIdempotencyKeyError,
    DuplicatePaymentError,
    OperationAlreadyPendingError,
    OperationId,
    OrderId,
    Payment,
    PaymentDomainEvent,
    PaymentId,
    PaymentModel,
    PaymentOperationRequested,
    crosses_snapshot_boundary,
)

from .event_codec import (
    SNAPSHOT_SCHEMA_VERSION,
    UnreadableStoredDataError,
    decode_event,
    decode_snapshot,
    encode_event,
    encode_snapshot,
)
from .event_store import (
    STREAM_VERSION_UNIQUE,
    StoredSnapshot,
    append_events,
    read_events,
    read_snapshot,
    write_snapshot,
)
from .replay_metrics import ReplayMetrics

PAYMENT_ORDER_UNIQUE = "payments_order_id_key"
OPERATION_KEY_UNIQUE = "payment_operations_idempotency_key_key"
OPERATION_PENDING_UNIQUE = "payment_operations_one_pending"
PROVIDER_CONSUMER = "payment-provider"
# ADR 37: an Outbox row is needed only until Debezium reads its insert from the WAL.
OUTBOX_RETENTION = timedelta(hours=1)

_PAYMENT_COLUMNS = "id, order_id, market_id, amount, currency, state, provider_reference, version"
_OPERATION_COLUMNS = (
    "id, payment_id, kind, idempotency_key, amount, currency, requested_at, state, "
    "provider_reference, decline_reason, completed_at"
)


def _constraint(error: errors.UniqueViolation) -> str | None:
    return error.diag.constraint_name


class DatabasePaymentRepository:
    """Payment aggregates in their streams (local ADR 2).

    ``payment_events`` is the source of truth; ``payments`` and ``payment_operations`` are
    projections written in the savepoint of the events and used to find a Payment by Order,
    operation or idempotency key. ``save`` passing a multiple of ``snapshot_every`` also
    writes the snapshot.
    """

    def __init__(
        self,
        database: Database,
        snapshot_every: int,
        replay_metrics: ReplayMetrics | None = None,
    ) -> None:
        self._db = database
        self._snapshot_every = snapshot_every
        self._replay_metrics = replay_metrics or ReplayMetrics()

    def get(self, payment_id: PaymentId) -> Payment | None:
        """Return the Payment from its snapshot and stream, or ``None`` without a stream.

        Raises:
            UnreadableStoredDataError: A stored event or a snapshot of the current schema
                cannot be read, or the versions of the stream have a gap.
        """
        with self._db.connection() as conn:
            # Сначала снапшот: поток только дописывается, более позднее чтение хвоста ничего
            # не пропустит.
            snapshot = _usable_snapshot(read_snapshot(conn, payment_id))
            after = 0 if snapshot is None else snapshot.version
            rows = read_events(conn, payment_id, after)
        if snapshot is None and not rows:
            return None
        for offset, row in enumerate(rows, start=1):
            if row.version != after + offset:
                raise UnreadableStoredDataError(f"stream {payment_id} has a gap at {row.version}")
        events = [decode_event(r.event_type, r.schema_version, r.payload) for r in rows]
        self._replay_metrics.loaded(len(events), from_snapshot=snapshot is not None)
        model = None if snapshot is None else decode_snapshot(snapshot.state)
        try:
            return Payment.from_history(model, after, events)
        except ValueError as error:
            raise UnreadableStoredDataError(f"stream {payment_id}: {error}") from error

    def get_by_order(self, order_id: OrderId) -> Payment | None:
        """Return the only Payment of the Order, or ``None``."""
        return self._get_through("SELECT id FROM payments WHERE order_id = %s", order_id)

    def get_by_operation(self, operation_id: OperationId) -> Payment | None:
        """Return the Payment holding the operation, or ``None``."""
        return self._get_through(
            "SELECT payment_id FROM payment_operations WHERE id = %s", operation_id
        )

    def get_by_idempotency_key(self, idempotency_key: str) -> Payment | None:
        """Return the Payment holding the operation with the key, or ``None``."""
        return self._get_through(
            "SELECT payment_id FROM payment_operations WHERE idempotency_key = %s", idempotency_key
        )

    def add(self, payment: Payment) -> int:
        """Persist a new Payment and return its version.

        Raises:
            DuplicatePaymentError: the Order already has a Payment.
        """
        return self.save(payment)

    def save(self, payment: Payment) -> int:
        """Append the new events with the projections and return the committed version.

        Raises:
            ConcurrencyConflictError: the stream has moved on since the Payment was loaded.
            DuplicatePaymentError: the Order already has another Payment.
            DuplicateIdempotencyKeyError: another Payment uses an operation key.
            OperationAlreadyPendingError: the projection already has a pending operation.
        """
        events = payment.uncommitted_events
        if not events:
            return payment.version
        version = payment.version + len(events)
        with self._db.connection() as conn:
            try:
                with conn.transaction():
                    append_events(
                        conn, payment.id, payment.version, [encode_event(e) for e in events]
                    )
                    project_payment(conn, payment.model, version)
                    if crosses_snapshot_boundary(payment.version, version, self._snapshot_every):
                        write_snapshot(
                            conn,
                            payment.id,
                            version,
                            SNAPSHOT_SCHEMA_VERSION,
                            encode_snapshot(payment.model),
                        )
            except errors.UniqueViolation as error:
                raise _mapped(error, payment) from error
        payment.mark_committed(version)
        return version

    def _get_through(self, query: str, value: str) -> Payment | None:
        with self._db.connection() as conn:
            row = conn.execute(query, (value,)).fetchone()
        return None if row is None else self.get(PaymentId(row[0]))


def _usable_snapshot(snapshot: StoredSnapshot | None) -> StoredSnapshot | None:
    # Другая версия схемы — состояние собирается из всего потока, следующая граница
    # перезапишет снапшот. Это пересборка производных данных, источник истины тот же.
    if snapshot is None or snapshot.schema_version != SNAPSHOT_SCHEMA_VERSION:
        return None
    return snapshot


def _mapped(error: errors.UniqueViolation, payment: Payment) -> Exception:
    constraint = _constraint(error)
    if constraint == STREAM_VERSION_UNIQUE:
        return ConcurrencyConflictError("Payment", payment.id)
    if constraint == PAYMENT_ORDER_UNIQUE:
        return DuplicatePaymentError(payment.order_id)
    if constraint == OPERATION_KEY_UNIQUE:
        key = _uncommitted_key(payment)
        return DuplicateIdempotencyKeyError(key)
    if constraint == OPERATION_PENDING_UNIQUE:
        return OperationAlreadyPendingError(payment.id)
    return error


def _uncommitted_key(payment: Payment) -> str:
    return next(
        event.idempotency_key
        for event in payment.uncommitted_events
        if isinstance(event, PaymentOperationRequested)
    )


def project_payment(conn: psycopg.Connection, payment: PaymentModel, version: int) -> None:
    """Write the projections of the Payment state at ``version`` on the connection."""
    conn.execute(
        f"INSERT INTO payments ({_PAYMENT_COLUMNS}) "  # noqa: S608 - only fixed column/condition constants are interpolated
        "VALUES (%s, %s, %s, %s, %s, %s, %s, %s) "
        "ON CONFLICT (id) DO UPDATE SET state = EXCLUDED.state, "
        "provider_reference = EXCLUDED.provider_reference, version = EXCLUDED.version",
        (
            payment.id,
            payment.order_id,
            payment.market_id.value,
            payment.amount.amount,
            payment.amount.currency,
            payment.state.value,
            payment.provider_reference,
            version,
        ),
    )
    if not payment.operations:
        return
    with conn.cursor() as cursor:
        cursor.executemany(
            f"INSERT INTO payment_operations ({_OPERATION_COLUMNS}) "  # noqa: S608 - only fixed column/condition constants are interpolated
            "VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s) "
            "ON CONFLICT (id) DO UPDATE SET state = EXCLUDED.state, "
            "provider_reference = EXCLUDED.provider_reference, "
            "decline_reason = EXCLUDED.decline_reason, completed_at = EXCLUDED.completed_at",
            [
                (
                    operation.id,
                    operation.payment_id,
                    operation.kind.value,
                    operation.idempotency_key,
                    operation.amount.amount,
                    operation.amount.currency,
                    operation.requested_at,
                    operation.state.value,
                    operation.provider_reference,
                    operation.decline_reason,
                    operation.completed_at,
                )
                for operation in payment.operations
            ],
        )


class DatabasePaymentOutbox:
    """Writes Integration Events into ``payments_outbox`` inside the active Unit of Work.

    Debezium reads the inserts from the WAL (ADR 37); a row carries every field of the
    Kafka message. The injected ``mapper`` decides which Domain Events have a published
    contract; events it maps to ``None`` are not written.
    """

    def __init__(self, database: Database, mapper: OutboxEventMapper) -> None:
        self._db = database
        self._mapper = mapper

    def add(self, aggregate: AggregateRef, events: Sequence[PaymentDomainEvent]) -> None:
        """Write the mapped events with the current ``traceparent``; unmapped events are skipped.

        Rows older than ``OUTBOX_RETENTION`` are deleted in the same transaction: the WAL
        keeps an insert for Debezium independently of the row, and the publication
        ignores deletes.
        """
        messages = [m for m in (self._mapper.map(aggregate, e) for e in events) if m is not None]
        if not messages:
            return
        traceparent = outbox_traceparent()
        with self._db.connection() as conn, conn.cursor() as cursor:
            # SKIP LOCKED: concurrent Units of Work do not wait on the same old rows.
            cursor.execute(
                "DELETE FROM payments_outbox WHERE event_id IN ("
                "SELECT event_id FROM payments_outbox WHERE created_at < now() - %s "
                "FOR UPDATE SKIP LOCKED)",
                (OUTBOX_RETENTION,),
            )
            cursor.executemany(
                "INSERT INTO payments_outbox (event_id, event_type, aggregate_type, "
                "aggregate_id, aggregate_version, occurred_at, topic, payload, traceparent) "
                "VALUES (%s, %s, %s, %s, %s, now(), %s, %s, %s)",
                [
                    (
                        outbox_event_id(aggregate, m.event_type),
                        m.event_type,
                        aggregate.type,
                        aggregate.id,
                        aggregate.version,
                        m.topic,
                        m.payload,
                        traceparent,
                    )
                    for m in messages
                ],
            )


class DatabaseProviderMessageInbox:
    """Provider callback deduplication in the shared ``inbox`` table."""

    def __init__(self, database: Database, consumer: str = PROVIDER_CONSUMER) -> None:
        self._db = database
        self._consumer = consumer

    def register(self, provider_message_id: str) -> bool:
        """Record the provider message ID; ``False`` when it was already processed."""
        with self._db.connection() as conn:
            row = conn.execute(
                "INSERT INTO inbox (consumer, event_id) VALUES (%s, %s) "
                "ON CONFLICT DO NOTHING RETURNING 1",
                (self._consumer, provider_message_id),
            ).fetchone()
        return row is not None

"""PostgreSQL adapters of the payment repositories, Outbox and provider message Inbox.

Inserts run in a savepoint so that a mapped unique violation leaves the surrounding
Unit of Work usable. ``save`` updates only the loaded ``version``; zero affected rows
means a concurrent modification.
"""

from collections.abc import Sequence
from typing import Any

from psycopg import errors
from psycopg.types.json import Jsonb

from pkg.application import AggregateRef, ConcurrencyConflictError
from slices.payment.domains.payment import (
    DuplicateIdempotencyKeyError,
    DuplicatePaymentError,
    Money,
    OperationAlreadyPendingError,
    OperationId,
    OperationKind,
    OperationState,
    OrderId,
    Payment,
    PaymentDomainEvent,
    PaymentId,
    PaymentOperation,
    PaymentState,
)

from .database import PostgresDatabase
from .outbox import OutboxEventMapper, outbox_event_id, outbox_metadata

PAYMENT_ORDER_UNIQUE = "payments_order_id_key"
OPERATION_KEY_UNIQUE = "payment_operations_idempotency_key_key"
OPERATION_PENDING_UNIQUE = "payment_operations_one_pending"
PROVIDER_CONSUMER = "payment-provider"

_PAYMENT_COLUMNS = "id, order_id, amount, currency, state, provider_reference, version"
_OPERATION_COLUMNS = (
    "id, payment_id, kind, idempotency_key, amount, currency, requested_at, state, "
    "provider_reference, decline_reason, completed_at, version"
)


def _constraint(error: errors.UniqueViolation) -> str | None:
    return error.diag.constraint_name


class PostgresPaymentRepository:
    """Payment aggregates in the ``payments`` table with optimistic locking by ``version``."""

    def __init__(self, database: PostgresDatabase) -> None:
        self._db = database

    def get(self, payment_id: PaymentId) -> Payment | None:
        """Return the Payment, or ``None`` when it does not exist."""
        return self._one(f"SELECT {_PAYMENT_COLUMNS} FROM payments WHERE id = %s", payment_id)  # noqa: S608 - only fixed column/condition constants are interpolated

    def get_by_order(self, order_id: OrderId) -> Payment | None:
        """Return the only Payment of the Order, or ``None``."""
        return self._one(f"SELECT {_PAYMENT_COLUMNS} FROM payments WHERE order_id = %s", order_id)  # noqa: S608 - only fixed column/condition constants are interpolated

    def add(self, payment: Payment) -> int:
        """Insert a new Payment at version 1 and return that version.

        Raises:
            DuplicatePaymentError: the Order already has a Payment.
        """
        with self._db.connection() as conn:
            try:
                with conn.transaction():
                    conn.execute(
                        f"INSERT INTO payments ({_PAYMENT_COLUMNS}) "  # noqa: S608 - only fixed column/condition constants are interpolated
                        "VALUES (%s, %s, %s, %s, %s, %s, 1)",
                        (
                            payment.id,
                            payment.order_id,
                            payment.amount.amount,
                            payment.amount.currency,
                            payment.state.value,
                            payment.provider_reference,
                        ),
                    )
            except errors.UniqueViolation as error:
                if _constraint(error) == PAYMENT_ORDER_UNIQUE:
                    raise DuplicatePaymentError(payment.order_id) from error
                raise
        return 1

    def save(self, payment: Payment) -> int:
        """Persist the Payment state if its loaded version is current; return the new version.

        Raises:
            ConcurrencyConflictError: the Payment was modified concurrently.
        """
        with self._db.connection() as conn:
            row = conn.execute(
                "UPDATE payments SET state = %s, provider_reference = %s, version = version + 1 "
                "WHERE id = %s AND version = %s RETURNING version",
                (payment.state.value, payment.provider_reference, payment.id, payment.version),
            ).fetchone()
        if row is None:
            raise ConcurrencyConflictError("Payment", payment.id)
        return int(row[0])

    def _one(self, query: str, value: str) -> Payment | None:
        with self._db.connection() as conn:
            row = conn.execute(query, (value,)).fetchone()
        if row is None:
            return None
        payment_id, order_id, amount, currency, state, reference, version = row
        return Payment.restore(
            PaymentId(payment_id),
            OrderId(order_id),
            Money(amount, currency),
            PaymentState(state),
            reference,
            int(version),
        )


class PostgresPaymentOperationRepository:
    """Payment Operations in the ``payment_operations`` table with optimistic locking."""

    def __init__(self, database: PostgresDatabase) -> None:
        self._db = database

    def get(self, operation_id: OperationId) -> PaymentOperation | None:
        """Return the Payment Operation, or ``None``."""
        return self._first("id = %s", operation_id)

    def get_by_idempotency_key(self, idempotency_key: str) -> PaymentOperation | None:
        """Return the Payment Operation registered under the key, or ``None``."""
        return self._first("idempotency_key = %s", idempotency_key)

    def find_pending(self, payment_id: PaymentId) -> PaymentOperation | None:
        """Return the pending Payment Operation of the Payment; there is at most one."""
        return self._first("payment_id = %s AND state = 'pending'", payment_id)

    def list_for_payment(self, payment_id: PaymentId) -> list[PaymentOperation]:
        """Return every Payment Operation of the Payment in request order."""
        with self._db.connection() as conn:
            rows = conn.execute(
                f"SELECT {_OPERATION_COLUMNS} FROM payment_operations "  # noqa: S608 - only fixed column/condition constants are interpolated
                "WHERE payment_id = %s ORDER BY requested_at, id",
                (payment_id,),
            ).fetchall()
        return [_operation(row) for row in rows]

    def add(self, operation: PaymentOperation) -> None:
        """Insert a new pending Payment Operation at version 1.

        Raises:
            DuplicateIdempotencyKeyError: the idempotency key is already used.
            OperationAlreadyPendingError: the Payment already has a pending operation.
        """
        with self._db.connection() as conn:
            try:
                with conn.transaction():
                    conn.execute(
                        f"INSERT INTO payment_operations ({_OPERATION_COLUMNS}) "  # noqa: S608 - only fixed column/condition constants are interpolated
                        "VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, 1)",
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
                        ),
                    )
            except errors.UniqueViolation as error:
                constraint = _constraint(error)
                if constraint == OPERATION_KEY_UNIQUE:
                    raise DuplicateIdempotencyKeyError(operation.idempotency_key) from error
                if constraint == OPERATION_PENDING_UNIQUE:
                    raise OperationAlreadyPendingError(operation.payment_id) from error
                raise

    def save(self, operation: PaymentOperation) -> int:
        """Persist the operation result if its loaded version is current; return the new version.

        Raises:
            ConcurrencyConflictError: the operation was modified concurrently.
        """
        with self._db.connection() as conn:
            row = conn.execute(
                "UPDATE payment_operations SET state = %s, provider_reference = %s, "
                "decline_reason = %s, completed_at = %s, version = version + 1 "
                "WHERE id = %s AND version = %s RETURNING version",
                (
                    operation.state.value,
                    operation.provider_reference,
                    operation.decline_reason,
                    operation.completed_at,
                    operation.id,
                    operation.version,
                ),
            ).fetchone()
        if row is None:
            raise ConcurrencyConflictError("PaymentOperation", operation.id)
        return int(row[0])

    def _first(self, condition: str, value: str) -> PaymentOperation | None:
        with self._db.connection() as conn:
            row = conn.execute(
                f"SELECT {_OPERATION_COLUMNS} FROM payment_operations WHERE {condition} LIMIT 1",  # noqa: S608 - only fixed column/condition constants are interpolated
                (value,),
            ).fetchone()
        return None if row is None else _operation(row)


def _operation(row: tuple[Any, ...]) -> PaymentOperation:
    (
        operation_id,
        payment_id,
        kind,
        key,
        amount,
        currency,
        requested_at,
        state,
        reference,
        reason,
        completed_at,
        version,
    ) = row
    return PaymentOperation.restore(
        OperationId(operation_id),
        PaymentId(payment_id),
        OperationKind(kind),
        key,
        Money(amount, currency),
        requested_at,
        OperationState(state),
        reference,
        reason,
        completed_at,
        int(version),
    )


class PostgresPaymentOutbox:
    """Writes Integration Events into ``outbox`` inside the active Unit of Work (ADR 31).

    The injected ``mapper`` decides which Domain Events have a published contract;
    events it maps to ``None`` are not written.
    """

    def __init__(self, database: PostgresDatabase, mapper: OutboxEventMapper) -> None:
        self._db = database
        self._mapper = mapper

    def add(self, aggregate: AggregateRef, events: Sequence[PaymentDomainEvent]) -> None:
        """Write the mapped events with the current ``traceparent``; unmapped events are skipped."""
        messages = [m for m in (self._mapper.map(aggregate, e) for e in events) if m is not None]
        if not messages:
            return
        metadata = Jsonb(outbox_metadata())
        with self._db.connection() as conn, conn.cursor() as cursor:
            cursor.executemany(
                "INSERT INTO outbox (event_id, event_type, aggregate_type, aggregate_id, "
                "aggregate_version, topic, payload, metadata) "
                "VALUES (%s, %s, %s, %s, %s, %s, %s, %s)",
                [
                    (
                        outbox_event_id(aggregate, m.event_type),
                        m.event_type,
                        aggregate.type,
                        aggregate.id,
                        aggregate.version,
                        m.topic,
                        m.payload,
                        metadata,
                    )
                    for m in messages
                ],
            )


class PostgresProviderMessageInbox:
    """Provider callback deduplication in the shared ``inbox`` table."""

    def __init__(self, database: PostgresDatabase, consumer: str = PROVIDER_CONSUMER) -> None:
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

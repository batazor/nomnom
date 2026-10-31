"""PostgreSQL adapters of the payment slice query ports."""

from dataclasses import dataclass
from datetime import datetime

from infrastructure.repository.database import Database
from slices.payment.applications.get_status import GetPaymentStatusResult
from slices.payment.applications.list_operations import PaymentOperationView
from slices.payment.domains.payment import (
    MarketId,
    OperationId,
    OperationKind,
    OperationRequester,
    OperationState,
    OrderId,
    PaymentId,
    PaymentOperationRequested,
    PaymentState,
)

from .event_codec import decode_event


class DatabasePaymentStatusReader:
    """Payment status read model over ``payments`` without provider data."""

    def __init__(self, database: Database) -> None:
        self._db = database

    def status(self, payment_id: PaymentId) -> GetPaymentStatusResult | None:
        """Return the view of the Payment, or ``None`` when it does not exist."""
        with self._db.connection() as conn:
            row = conn.execute(
                "SELECT order_id, market_id, amount, currency, state FROM payments WHERE id = %s",
                (payment_id,),
            ).fetchone()
        if row is None:
            return None
        order_id, market_id, amount, currency, state = row
        return GetPaymentStatusResult(
            payment_id=payment_id,
            order_id=OrderId(order_id),
            market_id=MarketId(market_id),
            amount=amount,
            currency=currency,
            state=PaymentState(state),
        )


class DatabasePaymentOperationHistory:
    """Operation history read model over ``payment_operations`` without provider data."""

    def __init__(self, database: Database) -> None:
        self._db = database

    def list_for_payment(self, payment_id: PaymentId) -> list[PaymentOperationView] | None:
        """Return the operation views of the Payment in request order, ``None`` without it."""
        with self._db.connection() as conn:
            exists = conn.execute("SELECT 1 FROM payments WHERE id = %s", (payment_id,)).fetchone()
            if exists is None:
                return None
            rows = conn.execute(
                "SELECT id, kind, state, amount, currency, idempotency_key, requested_at, "
                "completed_at, decline_reason FROM payment_operations "
                "WHERE payment_id = %s ORDER BY requested_at, id",
                (payment_id,),
            ).fetchall()
        return [
            PaymentOperationView(
                operation_id=OperationId(operation_id),
                kind=OperationKind(kind),
                state=OperationState(state),
                amount=amount,
                currency=currency,
                idempotency_key=key,
                requested_at=requested_at,
                completed_at=completed_at,
                decline_reason=reason,
            )
            for operation_id, kind, state, amount, currency, key, requested_at, completed_at, reason in rows
        ]


@dataclass(frozen=True)
class OperationAuditView:
    """An operation with its requester; ``None`` when the event predates the field."""

    operation_id: OperationId
    kind: OperationKind
    state: OperationState
    requested_at: datetime
    requested_by: OperationRequester | None


@dataclass(frozen=True)
class PaymentAuditView:
    """A Payment with its operations in request order, for the operator audit."""

    payment_id: PaymentId
    state: PaymentState
    operations: tuple[OperationAuditView, ...]


class DatabasePaymentAuditReader:
    """Audit read model: the projections plus the requester facts from the stream.

    The requester is not a part of the Payment state, since no decision uses it, so it is
    read from ``PaymentOperationRequested`` events and the aggregate is not restored.
    """

    def __init__(self, database: Database) -> None:
        self._db = database

    def history(self, payment_id: PaymentId) -> PaymentAuditView | None:
        """Return the view of the Payment, or ``None`` when it does not exist.

        Raises:
            UnreadableStoredDataError: A stored event cannot be read.
        """
        with self._db.connection() as conn:
            payment = conn.execute(
                "SELECT state FROM payments WHERE id = %s", (payment_id,)
            ).fetchone()
            if payment is None:
                return None
            operations = conn.execute(
                "SELECT id, kind, state, requested_at FROM payment_operations "
                "WHERE payment_id = %s ORDER BY requested_at, id",
                (payment_id,),
            ).fetchall()
            requests = conn.execute(
                "SELECT schema_version, payload FROM payment_events "
                "WHERE payment_id = %s AND event_type = 'PaymentOperationRequested'",
                (payment_id,),
            ).fetchall()
        requesters: dict[str, OperationRequester | None] = {}
        for schema_version, payload in requests:
            event = decode_event("PaymentOperationRequested", schema_version, payload)
            if isinstance(event, PaymentOperationRequested):
                requesters[event.operation_id] = event.requested_by
        return PaymentAuditView(
            payment_id,
            PaymentState(payment[0]),
            tuple(
                OperationAuditView(
                    OperationId(operation_id),
                    OperationKind(kind),
                    OperationState(state),
                    requested_at,
                    requesters.get(operation_id),
                )
                for operation_id, kind, state, requested_at in operations
            ),
        )

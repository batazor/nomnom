"""PostgreSQL adapters of the payment slice query ports."""

from infrastructure.repository.database import Database
from slices.payment.applications.get_payment_status import GetPaymentStatusResult
from slices.payment.applications.list_payment_operations import PaymentOperationView
from slices.payment.domains.payment import (
    MarketId,
    OperationId,
    OperationKind,
    OperationState,
    OrderId,
    PaymentId,
    PaymentState,
)


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

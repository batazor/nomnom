"""PostgreSQL adapters of the payment slice query ports."""

from slices.payment.applications.list_payment_operations import PaymentOperationView
from slices.payment.domains.payment import OperationId, OperationKind, OperationState, PaymentId

from .database import PostgresDatabase


class PostgresPendingOperationQuery:
    """Pending operation lookup over ``payment_operations`` (partial unique index)."""

    def __init__(self, database: PostgresDatabase) -> None:
        self._db = database

    def pending_operation_id(self, payment_id: PaymentId) -> OperationId | None:
        """Return the id of the pending operation of the Payment, or ``None``."""
        with self._db.connection() as conn:
            row = conn.execute(
                "SELECT id FROM payment_operations WHERE payment_id = %s AND state = 'pending'",
                (payment_id,),
            ).fetchone()
        return None if row is None else OperationId(row[0])


class PostgresPaymentOperationHistory:
    """Operation history read model over ``payment_operations`` without provider data."""

    def __init__(self, database: PostgresDatabase) -> None:
        self._db = database

    def list_for_payment(self, payment_id: PaymentId) -> list[PaymentOperationView]:
        """Return the operation views of the Payment in request order."""
        with self._db.connection() as conn:
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

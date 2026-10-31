"""Authorize Payment: reserve the full Payment amount for a later Capture.

A declined Authorization fails the Payment. When Authorization happens is decided by
the fulfillment process of ``orders``, not by ``payments``.
"""

from dataclasses import dataclass

from pkg.authz import Subject
from slices.payment.domains.payment import OperationKind

from ..operation_flow import PaymentOperationExecutor, PaymentOperationResult


@dataclass(frozen=True)
class AuthorizePaymentInput:
    """Request to authorize a Payment; the idempotency key identifies the Payment Operation."""

    subject: Subject
    payment_id: str
    idempotency_key: str


AuthorizePaymentResult = PaymentOperationResult


class AuthorizePaymentHandler:
    """Runs the Authorization of a Payment through the shared operation flow."""

    def __init__(self, executor: PaymentOperationExecutor) -> None:
        self._executor = executor

    def handle(self, command: AuthorizePaymentInput) -> AuthorizePaymentResult:
        """Authorize the full Payment amount.

        A repeated call with the same idempotency key returns the recorded result.
        See ``PaymentOperationExecutor.execute`` for the errors.
        """
        return self._executor.execute(
            command.subject, command.payment_id, command.idempotency_key, OperationKind.AUTHORIZE
        )

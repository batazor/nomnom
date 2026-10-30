"""Capture Payment: capture the full authorized amount.

A declined Capture keeps the Payment ``authorized``; partial capture is not modelled.
"""

from dataclasses import dataclass

from pkg.authz import Subject
from slices.payment.domains.payment import OperationKind

from ..operation_flow import PaymentOperationExecutor, PaymentOperationResult


@dataclass(frozen=True)
class CapturePaymentInput:
    """Request to capture a Payment; the idempotency key identifies the Payment Operation."""

    subject: Subject
    payment_id: str
    idempotency_key: str


CapturePaymentResult = PaymentOperationResult


class CapturePaymentHandler:
    """Runs the Capture of a Payment through the shared operation flow."""

    def __init__(self, executor: PaymentOperationExecutor) -> None:
        self._executor = executor

    def handle(self, command: CapturePaymentInput) -> CapturePaymentResult:
        """Capture the full authorized amount.

        A repeated call with the same idempotency key returns the recorded result.
        See ``PaymentOperationExecutor.execute`` for the errors.
        """
        return self._executor.execute(
            command.subject, command.payment_id, command.idempotency_key, OperationKind.CAPTURE
        )

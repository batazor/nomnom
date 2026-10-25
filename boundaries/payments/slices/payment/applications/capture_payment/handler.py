"""Capture Payment: capture the full authorized amount.

A declined Capture keeps the Payment ``authorized``; partial capture is not modelled.
"""

from dataclasses import dataclass

from pkg.application import Subject
from slices.payment.domains.payment import OperationKind

from ..operation_flow import PaymentOperationExecutor, PaymentOperationResult


@dataclass(frozen=True)
class CapturePaymentInput:
    subject: Subject
    payment_id: str
    idempotency_key: str


CapturePaymentResult = PaymentOperationResult


class CapturePaymentHandler:
    def __init__(self, executor: PaymentOperationExecutor) -> None:
        self._executor = executor

    def handle(self, command: CapturePaymentInput) -> CapturePaymentResult:
        return self._executor.execute(
            command.subject, command.payment_id, command.idempotency_key, OperationKind.CAPTURE
        )

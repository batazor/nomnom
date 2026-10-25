"""Refund Payment: return the full captured amount.

Partial refund is an open product question and is not supported. A declined Refund
keeps the Payment ``captured``.
"""

from dataclasses import dataclass

from pkg.application import Subject
from slices.payment.domains.payment import OperationKind

from ..operation_flow import PaymentOperationExecutor, PaymentOperationResult


@dataclass(frozen=True)
class RefundPaymentInput:
    subject: Subject
    payment_id: str
    idempotency_key: str


RefundPaymentResult = PaymentOperationResult


class RefundPaymentHandler:
    def __init__(self, executor: PaymentOperationExecutor) -> None:
        self._executor = executor

    def handle(self, command: RefundPaymentInput) -> RefundPaymentResult:
        return self._executor.execute(
            command.subject, command.payment_id, command.idempotency_key, OperationKind.REFUND
        )

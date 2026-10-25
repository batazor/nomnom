"""Cancel Authorization: release an authorized amount that will not be captured.

A declined cancellation keeps the Payment ``authorized``. The Order is not changed.
"""

from dataclasses import dataclass

from pkg.application import Subject
from slices.payment.domains.payment import OperationKind

from ..operation_flow import PaymentOperationExecutor, PaymentOperationResult


@dataclass(frozen=True)
class CancelAuthorizationInput:
    subject: Subject
    payment_id: str
    idempotency_key: str


CancelAuthorizationResult = PaymentOperationResult


class CancelAuthorizationHandler:
    def __init__(self, executor: PaymentOperationExecutor) -> None:
        self._executor = executor

    def handle(self, command: CancelAuthorizationInput) -> CancelAuthorizationResult:
        return self._executor.execute(
            command.subject,
            command.payment_id,
            command.idempotency_key,
            OperationKind.CANCEL_AUTHORIZATION,
        )

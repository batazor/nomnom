"""Typed application errors of the payment slice."""

from pkg.application import ApplicationError
from slices.payment.domains.payment import OperationId, OrderId, PaymentId, PaymentState


class InvalidPaymentRequestError(ApplicationError):
    """The request is malformed: amount, currency, idempotency key or provider result."""


class PaymentNotFoundError(ApplicationError):
    def __init__(self, payment_id: PaymentId) -> None:
        super().__init__(f"payment {payment_id} not found")
        self.payment_id = payment_id


class PaymentOperationNotFoundError(ApplicationError):
    def __init__(self, operation_id: OperationId) -> None:
        super().__init__(f"payment operation {operation_id} not found")
        self.operation_id = operation_id


class PaymentAlreadyExistsError(ApplicationError):
    def __init__(self, order_id: OrderId) -> None:
        super().__init__(f"payment for order {order_id} already exists with other terms")
        self.order_id = order_id


class PaymentStateConflictError(ApplicationError):
    def __init__(self, payment_id: PaymentId, state: PaymentState, operation: str) -> None:
        super().__init__(f"payment {payment_id} in state {state.value} does not allow {operation}")
        self.payment_id = payment_id
        self.state = state
        self.operation = operation


class OperationInProgressError(ApplicationError):
    def __init__(self, payment_id: PaymentId) -> None:
        super().__init__(f"payment {payment_id} has another operation in progress")
        self.payment_id = payment_id


class IdempotencyKeyConflictError(ApplicationError):
    def __init__(self, idempotency_key: str) -> None:
        super().__init__("idempotency key is already used for another payment operation")
        self.idempotency_key = idempotency_key


class ProviderResultConflictError(ApplicationError):
    def __init__(self, operation_id: OperationId, recorded: str | None, received: str) -> None:
        super().__init__(
            f"provider result {received} contradicts recorded result {recorded} "
            f"of operation {operation_id}"
        )
        self.operation_id = operation_id
        self.recorded = recorded
        self.received = received

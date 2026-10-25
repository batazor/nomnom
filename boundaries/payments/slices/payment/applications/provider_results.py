"""Applying final provider results to recorded Payment Operations at most once."""

from dataclasses import dataclass
from datetime import datetime
from enum import Enum
from typing import Optional

from pkg.application import AggregateRef, Clock
from slices.payment.domains.payment import (
    AlreadyInState,
    GuardFailed,
    InvalidTransition,
    OperationId,
    OperationOutcome,
    OperationState,
    Payment,
    PaymentDomainEvent,
    PaymentOperation,
    PaymentOperationRepository,
    PaymentRepository,
    PaymentState,
    complete_operation,
)

from .errors import (
    InvalidPaymentRequest,
    PaymentNotFound,
    PaymentOperationNotFound,
    PaymentStateConflict,
    ProviderResultConflict,
)
from .operation_result import _STATUS_OF, PAYMENT_AGGREGATE, OperationStatus
from .ports import PaymentOutbox, ProviderOperationOutcome, ProviderOperationStatus


@dataclass(frozen=True)
class AppliedProviderResult:
    operation: PaymentOperation
    payment_state: PaymentState
    applied: bool


class ProviderResultApplier:
    """Applies a final provider result to a known operation at most once.

    Must be called inside an active Unit of Work.
    """

    def __init__(
        self,
        payments: PaymentRepository,
        operations: PaymentOperationRepository,
        outbox: PaymentOutbox,
        clock: Clock,
    ) -> None:
        self._payments = payments
        self._operations = operations
        self._outbox = outbox
        self._clock = clock

    def apply(
        self, operation_id: OperationId, outcome: ProviderOperationOutcome
    ) -> AppliedProviderResult:
        domain_outcome = to_domain_outcome(outcome)
        operation = self._operations.get(operation_id)
        if operation is None:
            raise PaymentOperationNotFound(operation_id)
        payment = self._payments.get(operation.payment_id)
        if payment is None:
            raise PaymentNotFound(operation.payment_id)
        if operation.is_completed:
            received = (
                OperationState.SUCCEEDED if domain_outcome.succeeded else OperationState.DECLINED
            )
            if operation.state is not received:
                raise ProviderResultConflict(operation.id, operation.state.value, received.value)
            return AppliedProviderResult(operation, payment.state, applied=False)

        events = self._complete(payment, operation, domain_outcome, self._clock.now())
        self._operations.save(operation)
        version = self._payments.save(payment)
        self._outbox.add(AggregateRef(PAYMENT_AGGREGATE, payment.id, version), events)
        return AppliedProviderResult(operation, payment.state, applied=True)

    @staticmethod
    def _complete(
        payment: Payment,
        operation: PaymentOperation,
        outcome: OperationOutcome,
        completed_at: datetime,
    ) -> list[PaymentDomainEvent]:
        try:
            return complete_operation(payment, operation, outcome, completed_at)
        except (InvalidTransition, AlreadyInState) as error:
            raise PaymentStateConflict(payment.id, payment.state, operation.kind.value) from error
        except GuardFailed as error:
            raise InvalidPaymentRequest(f"provider result rejected: {error.reason}") from error


def to_domain_outcome(outcome: ProviderOperationOutcome) -> OperationOutcome:
    if outcome.status is ProviderOperationStatus.SUCCEEDED:
        return OperationOutcome.success(outcome.provider_reference)
    if outcome.status is ProviderOperationStatus.DECLINED:
        reason = (outcome.decline_reason or "").strip()
        if not reason:
            raise InvalidPaymentRequest("declined provider result requires a reason")
        return OperationOutcome.declined(reason)
    raise InvalidPaymentRequest("provider result must be final: succeeded or declined")


class ProviderResultStatus(str, Enum):
    APPLIED = "applied"
    ALREADY_APPLIED = "already_applied"
    DUPLICATE_MESSAGE = "duplicate_message"


@dataclass(frozen=True)
class ProviderResultApplication:
    """Outcome of applying an asynchronous provider result."""

    status: ProviderResultStatus
    operation_id: OperationId
    operation_status: Optional[OperationStatus] = None
    payment_state: Optional[PaymentState] = None

    @classmethod
    def of(cls, applied: AppliedProviderResult) -> "ProviderResultApplication":
        return cls(
            status=(
                ProviderResultStatus.APPLIED
                if applied.applied
                else ProviderResultStatus.ALREADY_APPLIED
            ),
            operation_id=applied.operation.id,
            operation_status=_STATUS_OF[applied.operation.state],
            payment_state=applied.payment_state,
        )

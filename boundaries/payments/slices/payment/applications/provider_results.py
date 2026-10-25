"""Applying final provider results to recorded Payment Operations at most once."""

from dataclasses import dataclass
from datetime import datetime
from enum import StrEnum

from pkg.application import AggregateRef, Clock
from slices.payment.domains.payment import (
    AlreadyInStateError,
    GuardFailedError,
    InvalidTransitionError,
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
    InvalidPaymentRequestError,
    PaymentNotFoundError,
    PaymentOperationNotFoundError,
    PaymentStateConflictError,
    ProviderResultConflictError,
)
from .operation_result import PAYMENT_AGGREGATE, STATUS_OF, OperationStatus
from .ports import PaymentOutbox, ProviderOperationOutcome, ProviderOperationStatus


@dataclass(frozen=True)
class AppliedProviderResult:
    """Operation and Payment state after applying a result; ``applied`` is ``False`` on a no-op."""

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
        """Apply the final result to the operation and its Payment and record outbox events.

        An equal result for a completed operation is a no-op (``applied=False``).

        Raises:
            InvalidPaymentRequestError: The outcome is not final or the Provider result
                is rejected by the Payment guard.
            PaymentOperationNotFoundError: The operation is unknown.
            PaymentNotFoundError: The Payment of the operation is missing.
            ProviderResultConflictError: The result contradicts the recorded one.
            PaymentStateConflictError: The Payment state does not allow the result.
        """
        domain_outcome = to_domain_outcome(outcome)
        operation = self._operations.get(operation_id)
        if operation is None:
            raise PaymentOperationNotFoundError(operation_id)
        payment = self._payments.get(operation.payment_id)
        if payment is None:
            raise PaymentNotFoundError(operation.payment_id)
        if operation.is_completed:
            received = (
                OperationState.SUCCEEDED if domain_outcome.succeeded else OperationState.DECLINED
            )
            if operation.state is not received:
                raise ProviderResultConflictError(
                    operation.id, operation.state.value, received.value
                )
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
        except (InvalidTransitionError, AlreadyInStateError) as error:
            raise PaymentStateConflictError(
                payment.id, payment.state, operation.kind.value
            ) from error
        except GuardFailedError as error:
            raise InvalidPaymentRequestError(f"provider result rejected: {error.reason}") from error


def to_domain_outcome(outcome: ProviderOperationOutcome) -> OperationOutcome:
    """Translate a final Provider answer into the domain outcome.

    Raises:
        InvalidPaymentRequestError: The answer is ``accepted`` or a decline has no reason.
    """
    if outcome.status is ProviderOperationStatus.SUCCEEDED:
        return OperationOutcome.success(outcome.provider_reference)
    if outcome.status is ProviderOperationStatus.DECLINED:
        reason = (outcome.decline_reason or "").strip()
        if not reason:
            raise InvalidPaymentRequestError("declined provider result requires a reason")
        return OperationOutcome.declined(reason)
    raise InvalidPaymentRequestError("provider result must be final: succeeded or declined")


class ProviderResultStatus(StrEnum):
    """How an asynchronous Provider result was handled."""

    APPLIED = "applied"
    ALREADY_APPLIED = "already_applied"
    DUPLICATE_MESSAGE = "duplicate_message"


@dataclass(frozen=True)
class ProviderResultApplication:
    """Outcome of applying an asynchronous provider result."""

    status: ProviderResultStatus
    operation_id: OperationId
    operation_status: OperationStatus | None = None
    payment_state: PaymentState | None = None

    @classmethod
    def of(cls, applied: AppliedProviderResult) -> ProviderResultApplication:
        """Build the application outcome from an applied or already applied result."""
        return cls(
            status=(
                ProviderResultStatus.APPLIED
                if applied.applied
                else ProviderResultStatus.ALREADY_APPLIED
            ),
            operation_id=applied.operation.id,
            operation_status=STATUS_OF[applied.operation.state],
            payment_state=applied.payment_state,
        )

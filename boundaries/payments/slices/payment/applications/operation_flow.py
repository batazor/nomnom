"""Shared flow of financial Payment Operations and of applying provider results.

Authorize, Capture, Cancel Authorization and Refund follow one sequence::

    check permission
    UoW#1 { find operation by idempotency key | check Payment and record pending operation }
    provider call with the same idempotency key       (outside of any transaction)
    UoW#2 { complete operation + Payment + Outbox }

A completed operation is returned as recorded without a provider call. A pending one
repeats the provider call with the same key, which the provider deduplicates.
"""

from dataclasses import dataclass
from datetime import datetime
from enum import Enum
from typing import Optional

from pkg.application import AggregateRef, Clock, Subject, UnitOfWork
from slices.payment.domains.payment import (
    PAYMENT_TRIGGER_OF,
    AlreadyInState,
    DuplicateIdempotencyKey,
    GuardFailed,
    InvalidTransition,
    OperationAlreadyPending,
    OperationId,
    OperationKind,
    OperationOutcome,
    OperationState,
    Payment,
    PaymentDomainEvent,
    PaymentId,
    PaymentOperation,
    PaymentOperationRepository,
    PaymentRepository,
    PaymentState,
    complete_operation,
)

from .access import require_permission
from .errors import (
    IdempotencyKeyConflict,
    InvalidPaymentRequest,
    OperationInProgress,
    PaymentNotFound,
    PaymentOperationNotFound,
    PaymentStateConflict,
    ProviderResultConflict,
)
from .ports import (
    PaymentIdentifiers,
    PaymentOutbox,
    PaymentPermission,
    PaymentPermissionChecker,
    PaymentProviderGateway,
    PaymentResource,
    ProviderOperationOutcome,
    ProviderOperationRequest,
    ProviderOperationStatus,
)

PAYMENT_AGGREGATE = "Payment"

_PERMISSION_OF: dict[OperationKind, PaymentPermission] = {
    OperationKind.AUTHORIZE: PaymentPermission.AUTHORIZE,
    OperationKind.CAPTURE: PaymentPermission.CAPTURE,
    OperationKind.CANCEL_AUTHORIZATION: PaymentPermission.CANCEL_AUTHORIZATION,
    OperationKind.REFUND: PaymentPermission.REFUND,
}


class OperationStatus(str, Enum):
    SUCCEEDED = "succeeded"
    DECLINED = "declined"
    PENDING = "pending"


_STATUS_OF: dict[OperationState, OperationStatus] = {
    OperationState.SUCCEEDED: OperationStatus.SUCCEEDED,
    OperationState.DECLINED: OperationStatus.DECLINED,
    OperationState.PENDING: OperationStatus.PENDING,
}


@dataclass(frozen=True)
class PaymentOperationResult:
    """Result of a financial operation; provider references are not exposed."""

    payment_id: PaymentId
    operation_id: OperationId
    kind: OperationKind
    status: OperationStatus
    payment_state: PaymentState
    decline_reason: Optional[str] = None
    replayed: bool = False

    @classmethod
    def of(
        cls, operation: PaymentOperation, payment_state: PaymentState, replayed: bool
    ) -> "PaymentOperationResult":
        return cls(
            payment_id=operation.payment_id,
            operation_id=operation.id,
            kind=operation.kind,
            status=_STATUS_OF[operation.state],
            payment_state=payment_state,
            decline_reason=operation.decline_reason,
            replayed=replayed,
        )


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


@dataclass(frozen=True)
class _Reservation:
    operation: PaymentOperation
    payment_state: PaymentState
    authorization_reference: Optional[str]


class PaymentOperationExecutor:
    def __init__(
        self,
        uow: UnitOfWork,
        payments: PaymentRepository,
        operations: PaymentOperationRepository,
        outbox: PaymentOutbox,
        provider: PaymentProviderGateway,
        permissions: PaymentPermissionChecker,
        clock: Clock,
        ids: PaymentIdentifiers,
    ) -> None:
        self._uow = uow
        self._payments = payments
        self._operations = operations
        self._provider = provider
        self._permissions = permissions
        self._clock = clock
        self._ids = ids
        self._applier = ProviderResultApplier(payments, operations, outbox, clock)

    def execute(
        self, subject: Subject, payment_id: str, idempotency_key: str, kind: OperationKind
    ) -> PaymentOperationResult:
        key = idempotency_key.strip()
        if not key:
            raise InvalidPaymentRequest("idempotency key is required")
        if not payment_id.strip():
            raise InvalidPaymentRequest("payment id is required")
        target = PaymentId(payment_id)
        require_permission(
            self._permissions, subject, _PERMISSION_OF[kind], PaymentResource(target)
        )

        reservation = self._reserve(target, key, kind)
        operation = reservation.operation
        if operation.is_completed:
            return PaymentOperationResult.of(operation, reservation.payment_state, replayed=True)

        outcome = self._provider.execute(
            ProviderOperationRequest(
                operation_id=operation.id,
                payment_id=operation.payment_id,
                kind=operation.kind,
                idempotency_key=operation.idempotency_key,
                amount=operation.amount,
                authorization_reference=reservation.authorization_reference,
            )
        )
        if outcome.status is ProviderOperationStatus.ACCEPTED:
            return PaymentOperationResult.of(operation, reservation.payment_state, replayed=False)

        applied = self._uow.run(lambda: self._applier.apply(operation.id, outcome))
        return PaymentOperationResult.of(
            applied.operation, applied.payment_state, replayed=not applied.applied
        )

    def _reserve(self, payment_id: PaymentId, key: str, kind: OperationKind) -> _Reservation:
        operation_id = self._ids.new_operation_id()
        requested_at = self._clock.now()

        def reserve() -> _Reservation:
            payment = self._payments.get(payment_id)
            if payment is None:
                raise PaymentNotFound(payment_id)
            existing = self._operations.get_by_idempotency_key(key)
            if existing is not None:
                if not existing.is_for(payment_id, kind):
                    raise IdempotencyKeyConflict(key)
                return _Reservation(existing, payment.state, payment.provider_reference)
            try:
                payment.ensure_can(PAYMENT_TRIGGER_OF[kind])
            except (InvalidTransition, AlreadyInState) as error:
                raise PaymentStateConflict(payment.id, payment.state, kind.value) from error
            if self._operations.find_pending(payment_id) is not None:
                raise OperationInProgress(payment_id)
            operation = PaymentOperation(
                operation_id, payment_id, kind, key, payment.amount, requested_at
            )
            self._operations.add(operation)
            return _Reservation(
                self._stored(operation.id), payment.state, payment.provider_reference
            )

        try:
            return self._uow.run(reserve)
        except DuplicateIdempotencyKey:
            pass
        except OperationAlreadyPending as error:
            raise OperationInProgress(payment_id) from error
        # A concurrent request recorded the same key first: continue with its operation.
        return self._uow.run(reserve)

    def _stored(self, operation_id: OperationId) -> PaymentOperation:
        operation = self._operations.get(operation_id)
        if operation is None:
            raise PaymentOperationNotFound(operation_id)
        return operation


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

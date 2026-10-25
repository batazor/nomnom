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

from pkg.application import Clock, Subject, UnitOfWork
from slices.payment.domains.payment import (
    PAYMENT_TRIGGER_OF,
    AlreadyInState,
    DuplicateIdempotencyKey,
    InvalidTransition,
    OperationAlreadyPending,
    OperationId,
    OperationKind,
    PaymentId,
    PaymentOperation,
    PaymentOperationRepository,
    PaymentRepository,
    PaymentState,
)

from .access import require_permission
from .errors import (
    IdempotencyKeyConflict,
    InvalidPaymentRequest,
    OperationInProgress,
    PaymentNotFound,
    PaymentOperationNotFound,
    PaymentStateConflict,
)
from .operation_result import PAYMENT_AGGREGATE, OperationStatus, PaymentOperationResult
from .ports import (
    PaymentIdentifiers,
    PaymentOutbox,
    PaymentPermission,
    PaymentPermissionChecker,
    PaymentProviderGateway,
    PaymentResource,
    ProviderOperationRequest,
    ProviderOperationStatus,
)
from .provider_results import (
    AppliedProviderResult,
    ProviderResultApplication,
    ProviderResultApplier,
    ProviderResultStatus,
    to_domain_outcome,
)

__all__ = [
    "PAYMENT_AGGREGATE",
    "AppliedProviderResult",
    "OperationStatus",
    "PaymentOperationExecutor",
    "PaymentOperationResult",
    "ProviderResultApplication",
    "ProviderResultApplier",
    "ProviderResultStatus",
    "to_domain_outcome",
]

_PERMISSION_OF: dict[OperationKind, PaymentPermission] = {
    OperationKind.AUTHORIZE: PaymentPermission.AUTHORIZE,
    OperationKind.CAPTURE: PaymentPermission.CAPTURE,
    OperationKind.CANCEL_AUTHORIZATION: PaymentPermission.CANCEL_AUTHORIZATION,
    OperationKind.REFUND: PaymentPermission.REFUND,
}


@dataclass(frozen=True)
class _Reservation:
    operation: PaymentOperation
    payment_state: PaymentState
    authorization_reference: str | None


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

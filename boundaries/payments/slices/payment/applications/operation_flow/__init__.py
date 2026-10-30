"""Shared flow of financial Payment Operations and of applying provider results.

Authorize, Capture, Cancel Authorization and Refund follow one sequence::

    check permission
    UoW#1 { find operation by idempotency key | Payment records the pending operation }
    provider call with the same idempotency key       (outside of any transaction)
    UoW#2 { Payment completes the operation + Outbox }

A completed operation is returned as recorded without a provider call. A pending one
repeats the provider call with the same key, which the provider deduplicates.
"""

from dataclasses import dataclass

from pkg.authz import Subject
from pkg.unit_of_work import UnitOfWork
from pkg.usecase import Clock
from slices.payment.domains.payment import (
    AlreadyInStateError,
    DuplicateIdempotencyKeyError,
    InvalidTransitionError,
    OperationAlreadyPendingError,
    OperationKind,
    PaymentId,
    PaymentOperation,
    PaymentRepository,
    PaymentState,
    rules_of,
)

from ..access import require_permission
from ..errors import (
    IdempotencyKeyConflictError,
    InvalidPaymentRequestError,
    OperationInProgressError,
    PaymentNotFoundError,
    PaymentStateConflictError,
)
from ..operation_result import PAYMENT_AGGREGATE, OperationStatus, PaymentOperationResult
from ..ports import (
    PaymentOperationIdSource,
    PaymentPermissionChecker,
    PaymentProviderGateway,
    PaymentResource,
    PaymentTransitionObserver,
    ProviderOperationRequest,
    ProviderOperationStatus,
)
from ..provider_results import (
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


@dataclass(frozen=True)
class _Reservation:
    operation: PaymentOperation
    payment_state: PaymentState
    authorization_reference: str | None


class PaymentOperationExecutor:
    """Executes Authorization, Capture, Cancel Authorization and Refund idempotently.

    The Provider is called outside of any Unit of Work, between recording the pending
    operation and applying its final result.
    """

    def __init__(
        self,
        uow: UnitOfWork,
        payments: PaymentRepository,
        provider: PaymentProviderGateway,
        permissions: PaymentPermissionChecker,
        clock: Clock,
        operation_ids: PaymentOperationIdSource,
        applier: ProviderResultApplier,
        transitions: PaymentTransitionObserver,
    ) -> None:
        self._uow = uow
        self._payments = payments
        self._provider = provider
        self._permissions = permissions
        self._clock = clock
        self._operation_ids = operation_ids
        self._applier = applier
        self._transitions = transitions

    def execute(
        self, subject: Subject, payment_id: str, idempotency_key: str, kind: OperationKind
    ) -> PaymentOperationResult:
        """Run one financial operation of ``kind`` for the Payment.

        Returns the recorded result without a Provider call when the key is already
        completed, and a ``pending`` result when the Provider accepted it asynchronously.

        Raises:
            InvalidPaymentRequestError: The payment id or idempotency key is blank.
            PermissionDeniedError: The subject may not perform the operation.
            PaymentNotFoundError: The Payment does not exist.
            IdempotencyKeyConflictError: The key belongs to another Payment or kind.
            PaymentStateConflictError: The Payment state does not allow the operation.
            OperationInProgressError: Another operation of the Payment has no final result.
            ProviderUnavailableError: The Provider could not be reached; retry with the same key.
        """
        key = idempotency_key.strip()
        if not key:
            raise InvalidPaymentRequestError("idempotency key is required")
        if not payment_id.strip():
            raise InvalidPaymentRequestError("payment id is required")
        target = PaymentId(payment_id)
        rules = rules_of(kind)
        require_permission(self._permissions, subject, rules.permission, PaymentResource(target))

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
        operation_id = self._operation_ids.new_operation_id()
        requested_at = self._clock.now()

        def reserve() -> _Reservation:
            payment = self._payments.get(payment_id)
            if payment is None:
                raise PaymentNotFoundError(payment_id)
            existing = payment.model.operation_by_key(key)
            if existing is not None:
                if not existing.is_for(payment_id, kind):
                    raise IdempotencyKeyConflictError(key)
                return _Reservation(existing, payment.state, payment.provider_reference)
            owner = self._payments.get_by_idempotency_key(key)
            # A key of this Payment missing from the loaded state means the state is stale:
            # saving conflicts on the stream and the Unit of Work retries with fresh state.
            if owner is not None and owner.id != payment_id:
                raise IdempotencyKeyConflictError(key)
            try:
                operation = payment.request_operation(operation_id, kind, key, requested_at)
            except InvalidTransitionError as error:
                self._transitions.rejected(error)
                raise PaymentStateConflictError(payment.id, payment.state, kind.value) from error
            except AlreadyInStateError as error:
                raise PaymentStateConflictError(payment.id, payment.state, kind.value) from error
            except OperationAlreadyPendingError as error:
                raise OperationInProgressError(payment_id) from error
            self._payments.save(payment)
            return _Reservation(operation, payment.state, payment.provider_reference)

        try:
            return self._uow.run(reserve)
        except DuplicateIdempotencyKeyError:
            pass
        except OperationAlreadyPendingError as error:
            raise OperationInProgressError(payment_id) from error
        # A concurrent request of another Payment recorded the key first: the next attempt
        # sees it. Requests of this Payment conflict on its stream and the Unit of Work
        # retries them.
        return self._uow.run(reserve)

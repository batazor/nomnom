"""Decider of a Payment (local ADR 2): pure ``decide`` and ``evolve`` over the state machine.

``decide`` turns a command and the current state into new events or a typed domain error.
``evolve`` applies a fact that already happened: it holds no business rules, so a recorded
stream always replays, even after the rules change.
"""

from dataclasses import dataclass, replace
from datetime import datetime

from .events import (
    PaymentAuthorizationCancelled,
    PaymentAuthorized,
    PaymentCaptured,
    PaymentCreated,
    PaymentDomainEvent,
    PaymentFailed,
    PaymentOperationDeclined,
    PaymentOperationRequested,
    PaymentOperationSucceeded,
    PaymentRefunded,
)
from .fsm import PaymentState, ensure_allowed
from .ids import OperationId, OperationKind, OperationRequester, OrderId, PaymentId
from .model import PaymentModel
from .operation import (
    OperationAlreadyPendingError,
    OperationMismatchError,
    OperationOutcome,
    OperationState,
    OperationTrigger,
    PaymentOperation,
    operation_transition,
)
from .operation_kinds import rules_of
from .vo.market import MarketId
from .vo.money import Money


@dataclass(frozen=True)
class CreatePayment:
    """Create the Payment of an Order in its Market."""

    payment_id: PaymentId
    order_id: OrderId
    market_id: MarketId
    amount: Money


@dataclass(frozen=True)
class RequestOperation:
    """Record a Payment Operation before calling the Provider."""

    operation_id: OperationId
    kind: OperationKind
    idempotency_key: str
    requested_at: datetime
    requested_by: OperationRequester


@dataclass(frozen=True)
class CompleteOperation:
    """Apply the final Provider result to a pending Payment Operation."""

    operation_id: OperationId
    outcome: OperationOutcome
    completed_at: datetime


PaymentCommand = CreatePayment | RequestOperation | CompleteOperation


def decide(state: PaymentModel | None, command: PaymentCommand) -> list[PaymentDomainEvent]:
    """Return the new events of ``command``; nothing changes on an error.

    Raises:
        ValueError: The Payment amount is not positive, it already exists or does not
            exist yet, the idempotency key or the decline reason is blank.
        InvalidTransitionError: The Payment state does not allow the operation.
        AlreadyInStateError: The operation repeats an already reached outcome.
        GuardFailedError: The Provider result has no required Provider Reference.
        OperationAlreadyPendingError: Another operation of the Payment has no final result.
        OperationMismatchError: The operation does not belong to the Payment.
        OperationAlreadyCompletedError: The operation already has this final result.
        InvalidOperationTransitionError: The operation already has the opposite result.
        UnknownOperationKindError: The operation kind has no rule.
    """
    match command:
        case CreatePayment():
            return _create(state, command)
        case RequestOperation():
            return _request(_existing(state), command)
        case CompleteOperation():
            return _complete(_existing(state), command)


def _create(state: PaymentModel | None, command: CreatePayment) -> list[PaymentDomainEvent]:
    if state is not None:
        raise ValueError("payment already exists")
    if command.amount.amount <= 0:
        raise ValueError("payment amount must be positive")
    return [PaymentCreated(command.payment_id, command.order_id, command.market_id, command.amount)]


def _request(payment: PaymentModel, command: RequestOperation) -> list[PaymentDomainEvent]:
    ensure_allowed(payment.state, rules_of(command.kind).trigger)
    if payment.has_pending_operation:
        raise OperationAlreadyPendingError(payment.id)
    if not command.idempotency_key.strip():
        raise ValueError("payment operation requires an idempotency key")
    return [
        PaymentOperationRequested(
            command.operation_id,
            payment.id,
            command.kind,
            command.idempotency_key,
            payment.amount,
            command.requested_at,
            command.requested_by,
        )
    ]


def _complete(payment: PaymentModel, command: CompleteOperation) -> list[PaymentDomainEvent]:
    operation = payment.operation(command.operation_id)
    if operation is None:
        raise OperationMismatchError(command.operation_id, payment.id)
    rules = rules_of(operation.kind)
    outcome = command.outcome
    if outcome.succeeded:
        operation_transition(operation.state, OperationTrigger.SUCCEED)
        reference = (outcome.provider_reference or "").strip() or None
        return [
            PaymentOperationSucceeded(
                operation.id, payment.id, operation.kind, reference, command.completed_at
            ),
            rules.on_success(payment, outcome.provider_reference),
        ]
    operation_transition(operation.state, OperationTrigger.DECLINE)
    reason = (outcome.decline_reason or "").strip()
    if not reason:
        raise ValueError("decline reason must not be empty")
    ensure_allowed(payment.state, rules.trigger)
    return [
        PaymentOperationDeclined(
            operation.id, payment.id, operation.kind, reason, command.completed_at
        ),
        *rules.on_decline(payment, reason),
    ]


def _existing(state: PaymentModel | None) -> PaymentModel:
    if state is None:
        raise ValueError("payment does not exist")
    return state


def evolve(state: PaymentModel | None, event: PaymentDomainEvent) -> PaymentModel:
    """Return the state after a recorded ``event``.

    Raises:
        ValueError: The stream does not start with ``PaymentCreated`` or ``event`` refers
            to an operation the stream did not request: the stream is corrupted.
    """
    if isinstance(event, PaymentCreated):
        if state is not None:
            raise ValueError("stream creates the payment twice")
        return PaymentModel(event.payment_id, event.order_id, event.market_id, event.amount)
    payment = _existing(state)
    match event:
        case PaymentOperationRequested():
            operation = PaymentOperation(
                event.operation_id,
                event.payment_id,
                event.kind,
                event.idempotency_key,
                event.amount,
                event.requested_at,
                requested_by=event.requested_by,
            )
            return replace(payment, operations=(*payment.operations, operation))
        case PaymentOperationSucceeded():
            return _with_result(
                payment,
                event.operation_id,
                state=OperationState.SUCCEEDED,
                provider_reference=event.provider_reference,
                completed_at=event.completed_at,
            )
        case PaymentOperationDeclined():
            return _with_result(
                payment,
                event.operation_id,
                state=OperationState.DECLINED,
                decline_reason=event.reason,
                completed_at=event.completed_at,
            )
        case PaymentAuthorized():
            return replace(
                payment,
                state=PaymentState.AUTHORIZED,
                provider_reference=event.provider_reference,
            )
        case PaymentCaptured():
            return replace(payment, state=PaymentState.CAPTURED)
        case PaymentAuthorizationCancelled():
            return replace(payment, state=PaymentState.PENDING, provider_reference=None)
        case PaymentRefunded():
            return replace(payment, state=PaymentState.REFUNDED)
        case PaymentFailed():
            return replace(payment, state=PaymentState.FAILED)


def _with_result(
    payment: PaymentModel,
    operation_id: OperationId,
    *,
    state: OperationState,
    completed_at: datetime,
    provider_reference: str | None = None,
    decline_reason: str | None = None,
) -> PaymentModel:
    if payment.operation(operation_id) is None:
        raise ValueError(f"stream has no requested operation {operation_id}")
    return replace(
        payment,
        operations=tuple(
            replace(
                operation,
                state=state,
                provider_reference=provider_reference,
                decline_reason=decline_reason,
                completed_at=completed_at,
            )
            if operation.id == operation_id
            else operation
            for operation in payment.operations
        ),
    )

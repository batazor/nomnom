"""Completing a Payment Operation: a domain service over the operation and its Payment."""

from datetime import datetime

from .events import PaymentDomainEvent
from .operation import (
    OperationAlreadyCompletedError,
    OperationMismatchError,
    OperationOutcome,
    PaymentOperation,
)
from .operation_kinds import rules_of
from .payment import Payment


def complete_operation(
    payment: Payment,
    operation: PaymentOperation,
    outcome: OperationOutcome,
    completed_at: datetime,
) -> list[PaymentDomainEvent]:
    """Apply a final provider result to the operation and the payment it belongs to.

    The rule of the operation kind (``OPERATION_KINDS``) decides the Payment effect: a
    successful operation performs the kind's Payment transition; a declined one checks
    that the Payment still allows the kind's trigger and applies the kind's decline
    effect (a declined Authorization fails the Payment, other declines keep its state).
    Returns the domain events recorded by both objects, operation events first.

    Raises:
        UnknownOperationKindError: The kind has no rule; neither object is changed.
    """
    if operation.payment_id != payment.id:
        raise OperationMismatchError(operation.id, payment.id)
    if operation.is_completed:
        raise OperationAlreadyCompletedError(operation.state)
    rules = rules_of(operation.kind)
    if outcome.succeeded:
        rules.on_success(payment, outcome.provider_reference)
        operation.succeed(outcome.provider_reference, completed_at)
    else:
        reason = (outcome.decline_reason or "").strip()
        if not reason:
            raise ValueError("decline reason must not be empty")
        payment.ensure_can(rules.trigger)
        rules.on_decline(payment, reason)
        operation.decline(reason, completed_at)
    events: list[PaymentDomainEvent] = []
    events.extend(operation.pull_events())
    events.extend(payment.pull_events())
    return events

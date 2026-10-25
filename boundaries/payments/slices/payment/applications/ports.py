"""Ports of external dependencies shared by the payment slice use cases."""

from collections.abc import Sequence
from dataclasses import dataclass
from enum import StrEnum
from typing import Protocol

from pkg.application import AggregateRef, ApplicationError, Subject
from slices.payment.domains.payment import (
    Money,
    OperationId,
    OperationKind,
    OrderId,
    PaymentDomainEvent,
    PaymentId,
)


class PaymentPermission(StrEnum):
    """Permissions on a Payment checked in the authorization graph."""

    CREATE = "create"
    AUTHORIZE = "authorize"
    CAPTURE = "capture"
    CANCEL_AUTHORIZATION = "cancel_authorization"
    REFUND = "refund"
    VIEW = "view"
    VIEW_OPERATIONS = "view_operations"


@dataclass(frozen=True)
class PaymentResource:
    """Payment the permission is checked on; ``order_id`` is known only at creation."""

    payment_id: PaymentId
    order_id: OrderId | None = None


class PaymentPermissionChecker(Protocol):
    """Authorization port; the SpiceDB adapter maps permissions and resources."""

    def is_allowed(
        self, subject: Subject, permission: PaymentPermission, resource: PaymentResource
    ) -> bool:
        """Return the decision; raise ``AuthorizationUnavailableError`` on error or timeout."""
        ...


class ProviderUnavailableError(ApplicationError):
    """The payment provider could not be reached or returned a technical error."""


class ProviderOperationStatus(StrEnum):
    """Provider answer to an operation request."""

    SUCCEEDED = "succeeded"
    DECLINED = "declined"
    ACCEPTED = "accepted"


@dataclass(frozen=True)
class ProviderOperationRequest:
    """Operation the Provider is asked to execute.

    ``authorization_reference`` is the Provider Reference of the current Authorization,
    needed by Capture, Cancel Authorization and Refund.
    """

    operation_id: OperationId
    payment_id: PaymentId
    kind: OperationKind
    idempotency_key: str
    amount: Money
    authorization_reference: str | None


@dataclass(frozen=True)
class ProviderOperationOutcome:
    """Provider answer; ``accepted`` means the final result arrives asynchronously."""

    status: ProviderOperationStatus
    provider_reference: str | None = None
    decline_reason: str | None = None

    @classmethod
    def succeeded(cls, provider_reference: str | None = None) -> ProviderOperationOutcome:
        """Build a final successful answer."""
        return cls(ProviderOperationStatus.SUCCEEDED, provider_reference=provider_reference)

    @classmethod
    def declined(cls, reason: str) -> ProviderOperationOutcome:
        """Build a final decline with the Provider's reason."""
        return cls(ProviderOperationStatus.DECLINED, decline_reason=reason)

    @classmethod
    def accepted(cls) -> ProviderOperationOutcome:
        """Build an answer whose final result arrives later by callback."""
        return cls(ProviderOperationStatus.ACCEPTED)


class PaymentProviderGateway(Protocol):
    """Port of the external Provider that executes Payment Operations."""

    def execute(self, request: ProviderOperationRequest) -> ProviderOperationOutcome:
        """Request the operation from the provider.

        The provider MUST deduplicate by ``idempotency_key``: a repeated request returns
        the original result without a second financial effect. ``operation_id`` is sent
        as the merchant reference used to correlate callbacks. Technical failures raise
        ``ProviderUnavailableError``.
        """
        ...


class PaymentOutbox(Protocol):
    """Transactional outbox for the facts published by the payments context."""

    def add(self, aggregate: AggregateRef, events: Sequence[PaymentDomainEvent]) -> None:
        """Record published facts in the active Unit of Work."""
        ...


class ProviderMessageInbox(Protocol):
    """Deduplication of Provider messages such as webhooks."""

    def register(self, provider_message_id: str) -> bool:
        """Record the message in the active Unit of Work; ``False`` for a duplicate."""
        ...


class PaymentIdentifiers(Protocol):
    """Source of new identifiers, so that use cases stay deterministic in tests."""

    def new_payment_id(self) -> PaymentId:
        """Return a new unique Payment identifier."""
        ...

    def new_operation_id(self) -> OperationId:
        """Return a new unique Payment Operation identifier."""
        ...

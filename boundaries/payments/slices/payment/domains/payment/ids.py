"""Identifiers and operation kinds shared by the payment model modules."""

from dataclasses import dataclass
from enum import StrEnum


class PaymentId(str):
    """Identifier of a Payment, assigned by the payments context."""


class OrderId(str):
    """Order ID from the Published Language; payments never imports the Order model."""


class OperationId(str):
    """Identifier of a Payment Operation, sent to the Provider as the merchant reference."""


class RequesterKind(StrEnum):
    """Whether a Payment Operation was requested by an end user or a service workload."""

    USER = "user"
    SERVICE = "service"


@dataclass(frozen=True)
class SealedSubject:
    """Identifier of a user encrypted under the key of that user; opaque to the domain."""

    key_id: str
    nonce: str
    ciphertext: str


@dataclass(frozen=True)
class OperationRequester:
    """Who requested a Payment Operation; ``delegate`` is the service acting for a user.

    A new user identifier is always sealed, a service identifier stays plain: a service is
    not a person. A plain user identifier exists only in streams older than sealing.
    """

    kind: RequesterKind
    id: str | SealedSubject
    delegate: str | None = None


class OperationKind(StrEnum):
    """Kind of Payment Operation the Provider is asked to perform."""

    AUTHORIZE = "authorize"
    CAPTURE = "capture"
    CANCEL_AUTHORIZATION = "cancel_authorization"
    REFUND = "refund"

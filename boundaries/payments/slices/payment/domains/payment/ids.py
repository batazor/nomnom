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
class OperationRequester:
    """Who requested a Payment Operation; ``delegate`` is the service acting for a user."""

    kind: RequesterKind
    id: str
    delegate: str | None = None


class OperationKind(StrEnum):
    """Kind of Payment Operation the Provider is asked to perform."""

    AUTHORIZE = "authorize"
    CAPTURE = "capture"
    CANCEL_AUTHORIZATION = "cancel_authorization"
    REFUND = "refund"

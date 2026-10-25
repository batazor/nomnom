"""Identifiers and operation kinds shared by the payment model modules."""

from enum import StrEnum


class PaymentId(str):
    """Identifier of a Payment, assigned by the payments context."""


class OrderId(str):
    """Order ID from the Published Language; payments never imports the Order model."""


class OperationId(str):
    """Identifier of a Payment Operation, sent to the Provider as the merchant reference."""


class OperationKind(StrEnum):
    """Kind of Payment Operation the Provider is asked to perform."""

    AUTHORIZE = "authorize"
    CAPTURE = "capture"
    CANCEL_AUTHORIZATION = "cancel_authorization"
    REFUND = "refund"

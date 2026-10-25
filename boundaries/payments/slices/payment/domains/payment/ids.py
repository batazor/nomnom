"""Identifiers and operation kinds shared by the payment model modules."""

from enum import Enum


class PaymentId(str):
    pass


class OrderId(str):
    pass


class OperationId(str):
    pass


class OperationKind(str, Enum):
    AUTHORIZE = "authorize"
    CAPTURE = "capture"
    CANCEL_AUTHORIZATION = "cancel_authorization"
    REFUND = "refund"

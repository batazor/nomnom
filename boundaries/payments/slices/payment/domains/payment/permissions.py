"""Permissions on a Payment, named by the payments model and checked by authorization."""

from enum import StrEnum


class PaymentPermission(StrEnum):
    """Permissions on a Payment checked in the authorization graph."""

    CREATE = "create"
    AUTHORIZE = "authorize"
    CAPTURE = "capture"
    CANCEL_AUTHORIZATION = "cancel_authorization"
    REFUND = "refund"
    VIEW = "view"
    VIEW_OPERATIONS = "view_operations"

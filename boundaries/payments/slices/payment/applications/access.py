"""Authoritative permission check performed before any protected domain operation."""

from pkg.application import PermissionDenied, Subject

from .ports import PaymentPermission, PaymentPermissionChecker, PaymentResource


def require_permission(
    checker: PaymentPermissionChecker,
    subject: Subject,
    permission: PaymentPermission,
    resource: PaymentResource,
) -> None:
    """Fail closed: a denial raises ``PermissionDenied``; checker errors propagate."""
    if not checker.is_allowed(subject, permission, resource):
        raise PermissionDenied(permission.value)

"""Authoritative permission check performed before any protected domain operation."""

from pkg.authz import PermissionDeniedError, Subject

from .ports import PaymentPermission, PaymentPermissionChecker, PaymentResource


def require_permission(
    checker: PaymentPermissionChecker,
    subject: Subject,
    permission: PaymentPermission,
    resource: PaymentResource,
) -> None:
    """Fail closed: a denial raises ``PermissionDeniedError``; checker errors propagate."""
    if not checker.is_allowed(subject, permission, resource):
        raise PermissionDeniedError(permission.value)

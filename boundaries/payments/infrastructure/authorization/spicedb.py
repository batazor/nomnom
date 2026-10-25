"""SpiceDB adapter of ``PaymentPermissionChecker`` (ADR 18).

The mapping of payment permissions to SpiceDB checks is normative and shared with the
other contexts: ``openspec/changes/add-spicedb-authorization/design.md``.
"""

import logging
from typing import Any, Protocol

from authzed.api.v1 import (
    CheckPermissionRequest,
    CheckPermissionResponse,
    Consistency,
    InsecureClient,
    ObjectReference,
    SubjectReference,
    SyncClient,
)
from grpcutil import bearer_token_credentials

from pkg.application import AuthorizationUnavailable, Subject, SubjectKind
from pkg.config import SpiceDBSettings
from slices.payment.applications.ports import (
    PaymentPermission,
    PaymentPermissionChecker,
    PaymentResource,
)

logger = logging.getLogger(__name__)

PLATFORM = ObjectReference(object_type="platform", object_id="nomnom")
MANAGE_PAYMENTS = "manage_payments"
VIEW_PAYMENTS = "view_payments"

# Fulfillment commands address payments by order before resource-level relationships
# exist, so every payment permission is a capability of the platform singleton.
PERMISSION_MAPPING: dict[PaymentPermission, str] = {
    PaymentPermission.CREATE: MANAGE_PAYMENTS,
    PaymentPermission.AUTHORIZE: MANAGE_PAYMENTS,
    PaymentPermission.CAPTURE: MANAGE_PAYMENTS,
    PaymentPermission.CANCEL_AUTHORIZATION: MANAGE_PAYMENTS,
    PaymentPermission.REFUND: MANAGE_PAYMENTS,
    PaymentPermission.VIEW: VIEW_PAYMENTS,
    PaymentPermission.VIEW_OPERATIONS: VIEW_PAYMENTS,
}

_SUBJECT_TYPES: dict[SubjectKind, str] = {
    SubjectKind.USER: "user",
    SubjectKind.SERVICE: "service",
}


class PermissionsClient(Protocol):
    """The part of the authzed client used by the adapter."""

    def CheckPermission(  # noqa: N802 - authzed stub method name
        self, request: CheckPermissionRequest, *, timeout: float
    ) -> CheckPermissionResponse: ...


def check_request(
    subject: Subject, permission: PaymentPermission, resource: PaymentResource
) -> CheckPermissionRequest:
    """Build the ``CheckPermission`` request for a payment permission."""
    return CheckPermissionRequest(
        consistency=Consistency(minimize_latency=True),
        resource=PLATFORM,
        permission=PERMISSION_MAPPING[permission],
        subject=SubjectReference(
            object=ObjectReference(object_type=_SUBJECT_TYPES[subject.kind], object_id=subject.id),
        ),
    )


class SpiceDBPermissionChecker:
    """Fail closed: only ``HAS_PERMISSION`` allows; any error raises ``AuthorizationUnavailable``.

    ``NO_PERMISSION`` and ``CONDITIONAL_PERMISSION`` (missing caveat context) deny.
    """

    def __init__(self, client: PermissionsClient, timeout_seconds: float) -> None:
        if timeout_seconds <= 0:
            raise ValueError("SpiceDB timeout must be positive")
        self._client = client
        self._timeout = timeout_seconds

    def is_allowed(
        self, subject: Subject, permission: PaymentPermission, resource: PaymentResource
    ) -> bool:
        request = check_request(subject, permission, resource)
        try:
            response = self._client.CheckPermission(request, timeout=self._timeout)
        except Exception as error:
            # Details go to the log; the caller sees only "authorization unavailable".
            reason = _reason(error)
            logger.warning(
                "SpiceDB check %s for %s:%s failed: %s",
                request.permission,
                request.subject.object.object_type,
                request.subject.object.object_id,
                reason,
            )
            raise AuthorizationUnavailable("authorization unavailable") from error
        return (
            response.permissionship
            == CheckPermissionResponse.Permissionship.PERMISSIONSHIP_HAS_PERMISSION
        )


def _reason(error: Exception) -> str:
    """gRPC status code or exception type; messages may carry endpoint details."""
    code = getattr(error, "code", None)
    if callable(code):
        try:
            return str(code())
        except Exception:
            pass
    return type(error).__name__


class DenyAllPermissions:
    """Deny every permission while SpiceDB is not configured."""

    def is_allowed(
        self, subject: Subject, permission: PaymentPermission, resource: PaymentResource
    ) -> bool:
        return False


def create_spicedb_client(settings: SpiceDBSettings) -> Any:
    """Create the official authzed client; channels connect lazily on the first check."""
    if settings.insecure:
        return InsecureClient(settings.endpoint, settings.preshared_key)
    return SyncClient(settings.endpoint, bearer_token_credentials(settings.preshared_key))


class SpiceDBConfigurationError(RuntimeError):
    """The process checks permissions but SpiceDB is not configured."""


def create_permission_checker(settings: SpiceDBSettings | None) -> PaymentPermissionChecker:
    """Composition of the authorization port.

    ``None`` is the test composition without SpiceDB and denies everything. A
    process with settings requires ``SPICEDB__ENDPOINT`` and refuses to start
    without it.
    """
    if settings is None:
        return DenyAllPermissions()
    if not settings.enabled:
        raise SpiceDBConfigurationError(
            "SPICEDB__ENDPOINT is required: payments checks permissions in SpiceDB"
        )
    return SpiceDBPermissionChecker(
        create_spicedb_client(settings), settings.timeout.total_seconds()
    )

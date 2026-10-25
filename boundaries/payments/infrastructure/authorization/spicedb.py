"""SpiceDB adapter of ``PaymentPermissionChecker`` (ADR 18).

The mapping of payment permissions to SpiceDB checks is normative and shared with the
other contexts: ``openspec/changes/add-spicedb-authorization/design.md``.

Every check records ``nomnom.authz.checks`` and ``nomnom.authz.check.duration``
(``ops/semconv/model/authz.yaml``) with the SpiceDB ``nomnom.authz.permission`` and
``nomnom.authz.resource_type`` and ``nomnom.authz.result``: allowed | denied (business
result) | error | timeout (technical failure). Subject and resource IDs never become
attributes.
"""

import logging
import time
from enum import StrEnum
from typing import Protocol, cast

import grpc
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
from opentelemetry import metrics

from pkg.application import AuthorizationUnavailableError, Subject, SubjectKind
from pkg.config import SpiceDBSettings
from pkg.telemetry import SECONDS_BUCKETS
from pkg.telemetry.rpc import ClientTelemetryInterceptor, split_target
from slices.payment.applications.ports import (
    PaymentPermission,
    PaymentResource,
)

logger = logging.getLogger(__name__)

INSTRUMENTATION = "nomnom.payments.authz"

AUTHZ_PERMISSION = "nomnom.authz.permission"
AUTHZ_RESOURCE_TYPE = "nomnom.authz.resource_type"
AUTHZ_RESULT = "nomnom.authz.result"

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

    def CheckPermission(  # noqa: N802 - name fixed by the gRPC/specification contract
        self, request: CheckPermissionRequest, *, timeout: float
    ) -> CheckPermissionResponse:
        """Check one permission; ``timeout`` bounds the RPC in seconds."""
        ...


class CheckResult(StrEnum):
    """Closed ``nomnom.authz.result`` set of one check."""

    ALLOWED = "allowed"
    DENIED = "denied"
    ERROR = "error"
    TIMEOUT = "timeout"


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
    """Fail closed: only ``HAS_PERMISSION`` allows; any error raises ``AuthorizationUnavailableError``.

    ``NO_PERMISSION`` and ``CONDITIONAL_PERMISSION`` (missing caveat context) deny.
    """

    def __init__(
        self,
        client: PermissionsClient,
        timeout_seconds: float,
        meter_provider: metrics.MeterProvider | None = None,
    ) -> None:
        if timeout_seconds <= 0:
            raise ValueError("SpiceDB timeout must be positive")
        self._client = client
        self._timeout = timeout_seconds
        meter = metrics.get_meter(INSTRUMENTATION, meter_provider=meter_provider)
        self._checks = meter.create_counter(
            "nomnom.authz.checks",
            unit="{check}",
            description="Authorization checks by result.",
        )
        self._duration = meter.create_histogram(
            "nomnom.authz.check.duration",
            unit="s",
            description="Duration of one authorization check.",
            explicit_bucket_boundaries_advisory=list(SECONDS_BUCKETS),
        )

    def is_allowed(
        self, subject: Subject, permission: PaymentPermission, resource: PaymentResource
    ) -> bool:
        """Ask SpiceDB whether ``subject`` holds the platform capability of ``permission``.

        Raises:
            AuthorizationUnavailableError: SpiceDB failed or timed out; details are logged only.
        """
        request = check_request(subject, permission, resource)
        started = time.perf_counter()
        result = CheckResult.ERROR
        try:
            response = self._client.CheckPermission(request, timeout=self._timeout)
            allowed = (
                response.permissionship
                == CheckPermissionResponse.Permissionship.PERMISSIONSHIP_HAS_PERMISSION
            )
            result = CheckResult.ALLOWED if allowed else CheckResult.DENIED
            return allowed
        except Exception as error:
            result = _failure(error)
            # Details go to the log; the caller sees only "authorization unavailable".
            reason = _reason(error)
            logger.warning(
                "SpiceDB check %s for %s:%s failed: %s",
                request.permission,
                request.subject.object.object_type,
                request.subject.object.object_id,
                reason,
            )
            raise AuthorizationUnavailableError("authorization unavailable") from error
        finally:
            attributes = {
                AUTHZ_PERMISSION: request.permission,
                AUTHZ_RESOURCE_TYPE: request.resource.object_type,
                AUTHZ_RESULT: result.value,
            }
            self._checks.add(1, attributes)
            self._duration.record(time.perf_counter() - started, attributes)


def _failure(error: Exception) -> CheckResult:
    """``timeout`` for an expired deadline, ``error`` for any other failure."""
    if isinstance(error, TimeoutError):
        return CheckResult.TIMEOUT
    code = getattr(error, "code", None)
    if callable(code):
        try:
            if code() is grpc.StatusCode.DEADLINE_EXCEEDED:
                return CheckResult.TIMEOUT
        except Exception:  # noqa: BLE001 - a broken error object is still an error
            return CheckResult.ERROR
    return CheckResult.ERROR


def _reason(error: Exception) -> str:
    """Return the gRPC status code or the exception type; messages may carry endpoint details."""
    code = getattr(error, "code", None)
    if callable(code):
        try:
            return str(code())
        except Exception:  # noqa: BLE001, S110 - supervisor must catch every failure
            pass
    return type(error).__name__


def create_spicedb_client(
    settings: SpiceDBSettings, telemetry: ClientTelemetryInterceptor | None = None
) -> PermissionsClient:
    """Create the official authzed client; channels connect lazily on the first check.

    The channel passes the telemetry interceptor: client span, ``traceparent`` in the
    metadata and ``rpc.client.call.duration`` of every ``CheckPermission``.
    """
    interceptor = telemetry or ClientTelemetryInterceptor(*split_target(settings.endpoint))
    if settings.insecure:
        return cast(
            PermissionsClient,
            _traced(InsecureClient, interceptor)(settings.endpoint, settings.preshared_key),
        )
    return cast(
        PermissionsClient,
        _traced(SyncClient, interceptor)(
            settings.endpoint, bearer_token_credentials(settings.preshared_key)
        ),
    )


def _traced[C: type](client: C, interceptor: grpc.UnaryUnaryClientInterceptor) -> C:
    """Subclass of an authzed client whose stubs use the intercepted channel.

    The authzed clients build their channel inside ``__init__``; ``init_stubs`` is the
    only hook that sees it.
    """

    def init_stubs(self: object, channel: grpc.Channel) -> None:
        client.init_stubs(self, grpc.intercept_channel(channel, interceptor))  # type: ignore[attr-defined]

    return cast(C, type(f"Traced{client.__name__}", (client,), {"init_stubs": init_stubs}))

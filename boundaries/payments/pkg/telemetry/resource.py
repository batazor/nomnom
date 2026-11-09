"""Resource of the process: one identity for traces, metrics and logs (docs/observability.md)."""

from opentelemetry.sdk.resources import Resource

from pkg.config import TelemetrySettings

SERVICE_NAMESPACE = "nomnom"

# Resource attributes that identify the process in every signal; log records repeat them.
IDENTITY_ATTRIBUTES = (
    "service.namespace",
    "service.name",
    "service.version",
    "deployment.environment.name",
    "service.instance.id",
)


def build_resource(settings: TelemetrySettings) -> Resource:
    """Resource with ``service.namespace=nomnom``, ``service.name`` and the pod name.

    ``spec.telemetry.resourceAttributes`` adds ``service.version`` (Helm passes the image
    tag) and ``deployment.environment.name``; it cannot replace ``service.name`` or
    ``service.instance.id``. The resource is built explicitly: ``OTEL_RESOURCE_ATTRIBUTES``
    and ``OTEL_SERVICE_NAME`` are not read.
    """
    defaults = Resource(
        {
            "service.namespace": SERVICE_NAMESPACE,
            "telemetry.sdk.language": "python",
            "telemetry.sdk.name": "opentelemetry",
        }
    )
    return defaults.merge(Resource(dict(settings.resource_attributes))).merge(
        Resource(
            {
                "service.name": settings.service_name,
                "service.instance.id": settings.instance_id,
            }
        )
    )


def identity(resource: Resource) -> dict[str, str]:
    """Identity attributes of the resource that are set, for log records."""
    return {
        key: str(value)
        for key in IDENTITY_ATTRIBUTES
        if (value := resource.attributes.get(key)) not in (None, "")
    }

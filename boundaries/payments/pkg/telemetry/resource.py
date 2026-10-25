"""Resource of the process: one identity for traces, metrics and logs (docs/observability.md)."""

from collections.abc import Mapping
from urllib.parse import unquote

from opentelemetry.sdk.resources import Resource

SERVICE_NAMESPACE = "nomnom"

# Resource attributes that identify the process in every signal; log records repeat them.
IDENTITY_ATTRIBUTES = (
    "service.namespace",
    "service.name",
    "service.version",
    "deployment.environment.name",
    "service.instance.id",
)


def build_resource(service_name: str, environ: Mapping[str, str]) -> Resource:
    """Resource with ``service.namespace=nomnom`` and ``service.name`` of the process.

    ``OTEL_RESOURCE_ATTRIBUTES`` adds ``service.version``, ``service.instance.id`` and
    ``deployment.environment.name`` (Helm passes them); ``OTEL_SERVICE_NAME`` overrides
    the process name. ``environ`` is read explicitly so tests do not touch ``os.environ``.
    """
    defaults = Resource(
        {
            "service.namespace": SERVICE_NAMESPACE,
            "service.name": service_name,
            "telemetry.sdk.language": "python",
            "telemetry.sdk.name": "opentelemetry",
        }
    )
    from_env = _from_environ(environ)
    resource = defaults.merge(from_env)
    override = (environ.get("OTEL_SERVICE_NAME") or "").strip()
    if override:
        resource = resource.merge(Resource({"service.name": override}))
    return resource


def _from_environ(environ: Mapping[str, str]) -> Resource:
    """``OTEL_RESOURCE_ATTRIBUTES`` of ``environ``: ``key=value`` pairs, percent-encoded.

    Same format as the SDK ``OTELResourceDetector``, which reads only ``os.environ``.
    A malformed pair is skipped.
    """
    attributes: dict[str, str] = {}
    for pair in environ.get("OTEL_RESOURCE_ATTRIBUTES", "").split(","):
        key, sep, value = pair.partition("=")
        if sep and key.strip():
            attributes[key.strip()] = unquote(value.strip())
    return Resource(attributes)


def identity(resource: Resource) -> dict[str, str]:
    """Identity attributes of the resource that are set, for log records."""
    return {
        key: str(value)
        for key in IDENTITY_ATTRIBUTES
        if (value := resource.attributes.get(key)) not in (None, "")
    }

"""Versioned configuration file of a payments process (ADR 17).

The file at ``CONFIG_FILE`` is a Kubernetes-style resource that is not registered in the
Kubernetes API::

    apiVersion: config.nomnom.dev/v1alpha1
    kind: Payments
    metadata:
      name: payments
    spec: {...}

Every key of ``spec`` is camelCase; an unknown key at any level, the top level included,
is an error naming its path (``spec.shutdown.drainTimout: unknown field``). Secrets have
no key in ``spec``: they come only from env.
"""

import os
from collections.abc import Mapping
from pathlib import Path
from typing import Self

import yaml
from pydantic import BaseModel, ConfigDict, Field, ValidationError, model_validator

from .database import DatabaseSpec
from .event_store import EventStoreSpec
from .identity import IdentitySettings
from .provider import ProviderSpec
from .runtime import ManagementSettings, ServerSettings, ShutdownSettings
from .section import Section
from .spicedb import SpiceDBSpec
from .telemetry import LoggingSpec, MetricsSpec, TelemetrySpec, TracingSpec

CONFIG_FILE_ENV = "CONFIG_FILE"
API_VERSION = "config.nomnom.dev/v1alpha1"
KIND = "Payments"


class ConfigError(ValueError):
    """The configuration file or a secret is missing or invalid: the process stops."""


class Spec(Section):
    """``spec`` of the ``Payments`` kind: the sections of every payments process.

    A process requires only the sections it uses, but a key unknown to every process is
    an error.
    """

    server: ServerSettings = Field(default_factory=ServerSettings)
    management: ManagementSettings = Field(default_factory=ManagementSettings)
    shutdown: ShutdownSettings = Field(default_factory=ShutdownSettings)
    identity: IdentitySettings = Field(default_factory=IdentitySettings)
    spicedb: SpiceDBSpec = Field(default_factory=SpiceDBSpec)
    provider: ProviderSpec = Field(default_factory=ProviderSpec)
    database: DatabaseSpec = Field(default_factory=DatabaseSpec)
    event_store: EventStoreSpec
    telemetry: TelemetrySpec = Field(default_factory=TelemetrySpec)
    tracing: TracingSpec = Field(default_factory=TracingSpec)
    metrics: MetricsSpec = Field(default_factory=MetricsSpec)
    logging: LoggingSpec = Field(default_factory=LoggingSpec)

    @model_validator(mode="after")
    def _endpoint_for_export(self) -> Self:
        if (self.tracing.enabled or self.metrics.enabled) and not self.telemetry.endpoint:
            raise ValueError(
                "spec.telemetry.endpoint is required when spec.tracing.enabled or "
                "spec.metrics.enabled is true"
            )
        return self


class _Metadata(BaseModel):
    model_config = ConfigDict(extra="forbid")

    name: str


class _Document(BaseModel):
    """Top level of the file; ``spec`` is validated separately to report its path."""

    model_config = ConfigDict(extra="forbid", populate_by_name=False)

    api_version: str = Field(alias="apiVersion")
    kind: str
    metadata: _Metadata
    spec: dict[str, object]


def describe(error: ValidationError, prefix: str = "") -> str:
    """One line per error: the key path (``prefix`` first) and what is wrong with it.

    Input values are left out: they may hold secrets.
    """
    lines = []
    for item in error.errors(include_url=False, include_input=False, include_context=False):
        path = ".".join([prefix, *map(str, item["loc"])] if prefix else map(str, item["loc"]))
        message = "unknown field" if item["type"] == "extra_forbidden" else item["msg"]
        lines.append(f"{path}: {message}" if path else message)
    return "; ".join(lines)


def parse_spec(spec: Mapping[str, object]) -> Spec:
    """Validate ``spec`` of the file: camelCase keys only, an unknown key is an error.

    Raises:
        ConfigError: A key is unknown or a value is invalid; the message names the path.
    """
    try:
        return Spec.model_validate(spec, by_alias=True, by_name=False)
    except ValidationError as error:
        raise ConfigError(describe(error, "spec")) from None


def parse_document(document: object, role: str | None) -> Spec:
    """Check ``apiVersion``, ``kind`` and ``metadata.name`` of the file and parse ``spec``.

    ``role`` is the ``metadata.name`` the process expects; an operator command running
    in the pod of another role passes ``None`` and accepts any name.

    Raises:
        ConfigError: The document is not a ``Payments`` resource of this version and role,
            or its ``spec`` is invalid.
    """
    if not isinstance(document, Mapping):
        raise ConfigError("configuration file must be a mapping with apiVersion, kind and spec")
    if document.get("apiVersion") != API_VERSION or document.get("kind") != KIND:
        raise ConfigError(
            f"configuration file must have apiVersion {API_VERSION} and kind {KIND}, got "
            f"apiVersion {document.get('apiVersion')!r} and kind {document.get('kind')!r}"
        )
    try:
        parsed = _Document.model_validate(document)
    except ValidationError as error:
        raise ConfigError(describe(error)) from None
    if role is not None and parsed.metadata.name != role:
        raise ConfigError(
            f"metadata.name must be the process role {role!r}, got {parsed.metadata.name!r}"
        )
    return parse_spec(parsed.spec)


def load_spec(role: str | None) -> Spec:
    """Read the file at ``CONFIG_FILE`` and parse it (see ``parse_document``).

    Raises:
        ConfigError: ``CONFIG_FILE`` is empty, the file cannot be read or is not valid YAML,
            or the document is invalid.
    """
    path = os.environ.get(CONFIG_FILE_ENV)
    if path is None or not path.strip():
        raise ConfigError(f"{CONFIG_FILE_ENV} must point to the configuration file")
    try:
        document = yaml.safe_load(Path(path.strip()).read_text(encoding="utf-8"))
    except (OSError, yaml.YAMLError) as error:
        raise ConfigError(f"cannot read configuration file {path!r}: {error}") from None
    return parse_document(document, role)

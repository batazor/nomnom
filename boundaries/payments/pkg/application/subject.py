"""Verified caller identity passed into protected use cases."""

from dataclasses import dataclass, field
from enum import Enum


class SubjectKind(str, Enum):
    USER = "user"
    SERVICE = "service"


@dataclass(frozen=True)
class Subject:
    """Authenticated ``user`` or concrete ``service`` workload identity.

    Transport adapters build it only from a verified token or signed internal identity.
    ``delegate`` names the service that passed a ``user`` on its behalf (ADR 32); it is
    kept for logs and audit and does not take part in equality or authorization.
    """

    kind: SubjectKind
    id: str
    delegate: str | None = field(default=None, compare=False)

    def __post_init__(self) -> None:
        if not self.id.strip():
            raise ValueError("subject id must not be empty")

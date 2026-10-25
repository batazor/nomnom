"""Verified caller identity passed into protected use cases."""

from dataclasses import dataclass
from enum import Enum


class SubjectKind(str, Enum):
    USER = "user"
    SERVICE = "service"


@dataclass(frozen=True)
class Subject:
    """Authenticated ``user`` or concrete ``service`` workload identity.

    Transport adapters build it only from a verified token or signed internal identity.
    """

    kind: SubjectKind
    id: str

    def __post_init__(self) -> None:
        if not self.id.strip():
            raise ValueError("subject id must not be empty")

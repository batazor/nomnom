"""Reference of the aggregate whose committed version an Outbox record belongs to."""

from dataclasses import dataclass


@dataclass(frozen=True)
class AggregateRef:
    """Aggregate type, id and the committed version its published events belong to."""

    type: str
    id: str
    version: int

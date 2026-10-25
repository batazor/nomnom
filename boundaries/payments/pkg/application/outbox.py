"""Reference of the aggregate whose committed version an Outbox record belongs to."""

from dataclasses import dataclass


@dataclass(frozen=True)
class AggregateRef:
    type: str
    id: str
    version: int

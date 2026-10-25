"""Observability of the payments boundary recorded outside the domain layer.

Business counters (``domain``), Payment state machine transitions (``fsm``), telemetry
recorded after the outer commit (``committed``), the provider client (``provider``) and
the use case names with their error classification (``usecases``).
"""

from .committed import CommitScope, ObservedUnitOfWork
from .domain import DomainMetrics, ObservedOutbox
from .fsm import FsmMetrics
from .provider import ObservedProvider
from .usecases import classify_payment_error

__all__ = [
    "CommitScope",
    "DomainMetrics",
    "FsmMetrics",
    "ObservedOutbox",
    "ObservedProvider",
    "ObservedUnitOfWork",
    "classify_payment_error",
]

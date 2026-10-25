"""W3C context propagation with an allowlist of baggage keys (docs/observability.md#distributed-tracing).

``traceparent``/``tracestate`` are propagated as is; a malformed ``traceparent`` is
dropped by the W3C propagator, so the next span starts a new trace. Baggage is
untrusted input: only allowlisted keys are extracted and injected, the rest is dropped.
"""

from collections.abc import Iterable, Sequence
from typing import Any, override

from opentelemetry import baggage, propagate
from opentelemetry.baggage.propagation import W3CBaggagePropagator
from opentelemetry.context import Context
from opentelemetry.propagators.composite import CompositePropagator
from opentelemetry.propagators.textmap import (
    CarrierT,
    Getter,
    Setter,
    TextMapPropagator,
    default_getter,
    default_setter,
)
from opentelemetry.trace.propagation.tracecontext import TraceContextTextMapPropagator

# Ключи baggage, которые процесс принимает и передаёт дальше; пусто — baggage не
# распространяется. Новый ключ добавляется вместе с политикой данных для него.
BAGGAGE_ALLOWLIST: frozenset[str] = frozenset[str]()


class AllowlistBaggagePropagator(TextMapPropagator):
    """W3C Baggage restricted to ``allowed`` keys in both directions."""

    def __init__(self, allowed: Iterable[str]) -> None:
        self._allowed = frozenset(allowed)
        self._inner = W3CBaggagePropagator()

    @override
    def extract(
        self,
        carrier: CarrierT,
        context: Context | None = None,
        getter: Getter[CarrierT] = default_getter,
    ) -> Context:
        extracted = self._inner.extract(carrier, context, getter)
        return _restrict(extracted, self._allowed)

    @override
    def inject(
        self,
        carrier: CarrierT,
        context: Context | None = None,
        setter: Setter[CarrierT] = default_setter,
    ) -> None:
        self._inner.inject(carrier, _restrict(context, self._allowed), setter)

    @property
    @override
    def fields(self) -> set[str]:
        return self._inner.fields


def _restrict(context: Context | None, allowed: frozenset[str]) -> Context:
    current = baggage.get_all(context)
    for key in current:
        if key not in allowed:
            context = baggage.remove_baggage(key, context)
    return context if context is not None else Context()


def create_propagator(allowed_baggage: Iterable[str] = BAGGAGE_ALLOWLIST) -> TextMapPropagator:
    """W3C Trace Context plus allowlisted Baggage."""
    return CompositePropagator(
        [TraceContextTextMapPropagator(), AllowlistBaggagePropagator(allowed_baggage)]
    )


def install_propagator(allowed_baggage: Iterable[str] = BAGGAGE_ALLOWLIST) -> None:
    """Register the propagator globally; gRPC, HTTP and Outbox adapters use it."""
    propagate.set_global_textmap(create_propagator(allowed_baggage))


def current_traceparent(context: Context | None = None) -> str:
    """W3C ``traceparent`` of ``context`` (default: current), ``''`` without a valid span."""
    carrier: dict[str, str] = {}
    TraceContextTextMapPropagator().inject(carrier, context)
    return carrier.get("traceparent", "")


def metadata_carrier(metadata: Sequence[tuple[str, Any]] | None) -> dict[str, str]:
    """GRPC invocation metadata as a propagation carrier; binary values are skipped."""
    return {key: value for key, value in metadata or () if isinstance(value, str)}

"""CPython process metrics of the ``runtime`` inventory section (semconv process/cpython).

``process.cpu.time`` (``cpu.mode``), ``process.memory.usage``, ``process.thread.count``
and ``cpython.gc.collections`` (``cpython.gc.generation``) are observed at collection
time. They are written here instead of ``opentelemetry-instrumentation-system-metrics``:
that package still reports the pre-semconv attributes ``type`` and ``generation``, so
the inventory labels ``cpu_mode`` and ``cpython_gc_generation`` would be missing.
"""

import gc
import os
import threading
from collections.abc import Iterable

from opentelemetry.metrics import CallbackOptions, Meter, Observation

INSTRUMENTATION = "nomnom.payments.runtime"


def _cpu_time(_options: CallbackOptions) -> Iterable[Observation]:
    times = os.times()
    return [
        Observation(times.user, {"cpu.mode": "user"}),
        Observation(times.system, {"cpu.mode": "system"}),
    ]


def _memory_usage(_options: CallbackOptions) -> Iterable[Observation]:
    rss = _resident_bytes()
    observations: list[Observation] = [] if rss is None else [Observation(rss)]
    return observations


def _resident_bytes() -> int | None:
    """Resident set size: ``/proc/self/statm`` on Linux, ``ru_maxrss`` as a fallback."""
    try:
        with open("/proc/self/statm", encoding="ascii") as statm:  # noqa: PTH123
            return int(statm.read().split()[1]) * os.sysconf("SC_PAGE_SIZE")
    except OSError, ValueError, IndexError:
        pass
    try:
        import resource
        import sys

        peak = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
    except ImportError, OSError:
        return None
    # macOS отдаёт байты, Linux — килобайты; на Linux сюда попадаем только без /proc.
    return peak if sys.platform == "darwin" else peak * 1024


def _thread_count(_options: CallbackOptions) -> Iterable[Observation]:
    return [Observation(_os_threads() or threading.active_count())]


def _os_threads() -> int | None:
    """OS threads of the process (gRPC core and librdkafka included) on Linux."""
    try:
        with open("/proc/self/status", encoding="ascii") as status:  # noqa: PTH123
            for line in status:
                if line.startswith("Threads:"):
                    return int(line.split()[1])
    except OSError, ValueError, IndexError:
        return None
    return None


def _gc_collections(_options: CallbackOptions) -> Iterable[Observation]:
    return [
        Observation(stats["collections"], {"cpython.gc.generation": generation})
        for generation, stats in enumerate(gc.get_stats())
    ]


def register_runtime_metrics(meter: Meter) -> None:
    """Create the observable instruments on ``meter``."""
    meter.create_observable_counter(
        "process.cpu.time",
        callbacks=[_cpu_time],
        unit="s",
        description="Total CPU seconds broken down by CPU mode.",
    )
    meter.create_observable_up_down_counter(
        "process.memory.usage",
        callbacks=[_memory_usage],
        unit="By",
        description="The amount of physical memory in use.",
    )
    meter.create_observable_up_down_counter(
        "process.thread.count",
        callbacks=[_thread_count],
        unit="{thread}",
        description="Process threads count.",
    )
    meter.create_observable_counter(
        "cpython.gc.collections",
        callbacks=[_gc_collections],
        unit="{collection}",
        description="The number of times a generation was collected since interpreter start.",
    )

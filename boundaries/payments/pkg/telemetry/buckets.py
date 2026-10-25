"""Explicit histogram boundaries: the SDK defaults are milliseconds, durations here are seconds."""

# Same boundaries as orders ``telemetry.SecondsBuckets``: dashboards compare services.
SECONDS_BUCKETS: tuple[float, ...] = (
    0.005,
    0.01,
    0.025,
    0.05,
    0.1,
    0.25,
    0.5,
    1.0,
    2.5,
    5.0,
    10.0,
    30.0,
    60.0,
    300.0,
)

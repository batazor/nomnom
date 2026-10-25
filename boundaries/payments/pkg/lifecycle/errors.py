"""Errors that end the process lifecycle with a non-zero exit code."""


class LifecycleError(Exception):
    """Abnormal end of the process lifecycle; leads to a non-zero exit code."""


class DrainTimeoutError(LifecycleError):
    """Work in flight did not finish before the shutdown deadline."""

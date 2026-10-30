"""Handler port of the use cases: adapters depend on it, decorators wrap it."""

from typing import Protocol


class Handler[In, Out](Protocol):
    """One application use case: a Command or Query in, its result out.

    Positional-only, so the parameter name of a handler (``command``/``query``) is free.
    """

    def handle(self, request: In, /) -> Out:
        """Run the use case; typed application errors are raised, not returned."""
        ...

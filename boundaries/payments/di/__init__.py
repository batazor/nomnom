"""Dependency Injector containers of the payments processes (ADR 5).

One ``DeclarativeContainer`` per executable process selects implementations, assembles
the object graph and owns process resources: ``payments.PaymentsContainer`` (gRPC API
with the provider webhook worker; applies migrations at startup).
``resources`` holds the Resource initializers shared by the containers. Only
``entrypoints`` and tests import this package: ``slices`` and ``pkg`` receive dependencies
explicitly, nothing is wired.
"""

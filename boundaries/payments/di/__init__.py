"""Composition roots of the payments processes (ADR 5).

One module per executable process selects implementations, assembles the object graph
and owns process resources: ``payments`` (gRPC API with the provider webhook worker),
``relay`` (``payments-relay``) and ``migrate`` (``payments-migrate``). ``webhooks``
assembles the webhook worker of the ``payments`` process; ``resources`` holds the
resources shared by several processes. Only ``entrypoints`` import this package:
``slices`` and ``pkg`` receive their dependencies explicitly.
"""

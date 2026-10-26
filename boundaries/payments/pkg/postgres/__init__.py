"""PostgreSQL schema of the tables shared by every slice: ``migrations/*.sql``.

Owns the Transactional Outbox ``payments_outbox`` and the Inbox ``inbox``; the set is
applied before the slice sets (``di/resources.py``). Adapters live in
``infrastructure.postgres``.
"""

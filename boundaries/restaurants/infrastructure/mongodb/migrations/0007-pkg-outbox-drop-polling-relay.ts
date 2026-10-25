import type { Db } from "mongodb";

/**
 * Removes what the polling relay (ADR 31) left behind once CDC publishes the
 * Outbox (ADR 37): its indexes, the `seq` counter and lease collections and
 * the records written before, which the change stream never delivers
 * (`snapshot.mode=no_data`) and which have no `createdAt` for the TTL.
 * Idempotent: missing indexes and collections are skipped.
 */
export async function up(db: Db): Promise<void> {
  const outbox = db.collection("outbox");
  const indexes = new Set(
    (await outbox.listIndexes().toArray()).map((index) => index.name),
  );
  for (const name of ["outbox_seq_unique", "outbox_pending_by_seq"]) {
    if (indexes.has(name)) await outbox.dropIndex(name);
  }
  await outbox.deleteMany({ createdAt: { $exists: false } });
  const collections = new Set(
    (await db.listCollections({}, { nameOnly: true }).toArray()).map(
      (collection) => collection.name,
    ),
  );
  for (const name of ["outbox_counters", "outbox_leases"]) {
    if (collections.has(name)) await db.dropCollection(name);
  }
}

import type { Collection, Db } from "mongodb";

import type { DatabaseUnitOfWork } from "../../../../infrastructure/repository/unit-of-work.ts";
import type { ClosedPreparations } from "../../applications/record-wait-time/record-wait-time.ts";

type ClosedPreparationDocument = { _id: string; closedAt: Date };

export class DatabaseClosedPreparations implements ClosedPreparations {
  readonly #collection: Collection<ClosedPreparationDocument>;
  readonly #unitOfWork: DatabaseUnitOfWork;

  constructor(db: Db, unitOfWork: DatabaseUnitOfWork) {
    this.#collection = db.collection("closed_preparations");
    this.#unitOfWork = unitOfWork;
  }

  async has(orderId: string): Promise<boolean> {
    const closed = await this.#collection.findOne(
      { _id: orderId },
      { session: this.#unitOfWork.session, projection: { _id: 1 } },
    );
    return closed !== null;
  }

  async add(orderId: string, closedAt: Date): Promise<void> {
    await this.#collection.updateOne(
      { _id: orderId },
      { $setOnInsert: { closedAt } },
      { upsert: true, session: this.#unitOfWork.session },
    );
  }
}

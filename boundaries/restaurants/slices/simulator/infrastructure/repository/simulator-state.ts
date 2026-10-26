import type { Collection, Db } from "mongodb";

import type { SimulatorStateRepository } from "../../domains/simulator/simulator.ts";

export const SIMULATOR_COLLECTION = "simulator";
/** The only document: the Simulator state of this service. */
export const SIMULATOR_STATE_ID = "kitchen";
export const SIMULATOR_SCHEMA_VERSION = 1;

export type SimulatorStateDocument = {
  _id: string;
  schemaVersion: number;
  enabled: boolean;
  changedAt: Date;
};

/**
 * Single-document Simulator state (FR-604). An absent document is a disabled
 * Simulator, so a new database needs no seed.
 */
export class DatabaseSimulatorState implements SimulatorStateRepository {
  readonly #collection: Collection<SimulatorStateDocument>;

  constructor(db: Db) {
    this.#collection =
      db.collection<SimulatorStateDocument>(SIMULATOR_COLLECTION);
  }

  async isEnabled(): Promise<boolean> {
    const document = await this.#collection.findOne({
      _id: SIMULATOR_STATE_ID,
    });
    return document?.enabled === true;
  }

  async setEnabled(enabled: boolean, changedAt: Date): Promise<void> {
    await this.#collection.updateOne(
      { _id: SIMULATOR_STATE_ID },
      {
        $set: {
          schemaVersion: SIMULATOR_SCHEMA_VERSION,
          enabled,
          changedAt,
        },
      },
      { upsert: true },
    );
  }
}

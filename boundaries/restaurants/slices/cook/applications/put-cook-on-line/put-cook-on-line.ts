import {
  type ChangeCookDependencies,
  type CookCommand,
  changeCook,
} from "../change-cook/change-cook.ts";
import type { CookChangeResult } from "../cook-view/cook-view.ts";

export type PutCookOnLine = CookCommand;

/** The cook takes work: Start Preparation may assign them (FR-615). */
export class PutCookOnLineHandler {
  readonly #dependencies: ChangeCookDependencies;

  constructor(dependencies: ChangeCookDependencies) {
    this.#dependencies = dependencies;
  }

  execute(command: PutCookOnLine): Promise<CookChangeResult> {
    return changeCook(this.#dependencies, command, "put cook on line", (cook) =>
      cook.putOnLine(),
    );
  }
}

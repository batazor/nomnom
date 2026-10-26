import {
  type ChangeCookDependencies,
  type CookCommand,
  changeCook,
} from "../change-cook.ts";
import type { CookChangeResult } from "../cook-view.ts";

export type RetireCook = CookCommand;

/**
 * The cook leaves the Cook Pool and never returns; a cook on line stays with
 * `CookOnLineError` (FR-615). Orders keep the cook's id as history.
 */
export class RetireCookHandler {
  readonly #dependencies: ChangeCookDependencies;

  constructor(dependencies: ChangeCookDependencies) {
    this.#dependencies = dependencies;
  }

  execute(command: RetireCook): Promise<CookChangeResult> {
    return changeCook(
      this.#dependencies,
      command,
      "retire cook",
      (cook, _current, at) => cook.retire({ retiredAt: at }),
    );
  }
}

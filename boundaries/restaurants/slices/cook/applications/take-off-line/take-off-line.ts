import {
  type ChangeCookDependencies,
  type CookCommand,
  changeCook,
} from "../change/change.ts";
import type { CookChangeResult } from "../cook-view/cook-view.ts";

export type TakeCookOffLine = CookCommand;

/**
 * The cook stops taking work; a cook holding an order in Active Preparation
 * stays on line with `CookBusyError` (FR-615).
 */
export class TakeCookOffLineHandler {
  readonly #dependencies: ChangeCookDependencies;

  constructor(dependencies: ChangeCookDependencies) {
    this.#dependencies = dependencies;
  }

  execute(command: TakeCookOffLine): Promise<CookChangeResult> {
    return changeCook(
      this.#dependencies,
      command,
      "take cook off line",
      (cook, current) =>
        cook.takeOffLine({ holdsOrder: current !== undefined }),
    );
  }
}

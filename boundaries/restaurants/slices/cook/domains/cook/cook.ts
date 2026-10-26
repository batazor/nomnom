import {
  CookBusyError,
  CookNotOnLineError,
  CookOnLineError,
  CookRetiredError,
  InvalidCookError,
  InvalidCookReason,
} from "./errors.ts";

export * from "./errors.ts";

/** Longest name of a cook shown on the kitchen board. */
export const COOK_NAME_MAX_LENGTH = 64;

export type CookSnapshot = Readonly<{
  id: string;
  restaurantId: string;
  name: string;
  onLine: boolean;
  /** Moment of the last Start Preparation assigned to the cook. */
  lastAssignedAt?: Date;
  /** Set once the cook left the Cook Pool. */
  retiredAt?: Date;
}>;

const cookName = (name: string): string => {
  const normalized = name.trim();
  if (normalized.length === 0) {
    throw new InvalidCookError(InvalidCookReason.EmptyName);
  }
  if (normalized.length > COOK_NAME_MAX_LENGTH) {
    throw new InvalidCookError(InvalidCookReason.NameTooLong);
  }
  return normalized;
};

/**
 * Aggregate root of a cook of the Cook Pool of one restaurant: a unit of
 * Kitchen Capacity, not a subject. Whether the cook is busy is not stored
 * here: it holds an order while a Restaurant Order with its id is in Active
 * Preparation, so callers pass that fact in.
 */
export class Cook {
  readonly #id: string;
  readonly #restaurantId: string;
  readonly #name: string;
  readonly #version: number;
  #onLine: boolean;
  #lastAssignedAt: Date | undefined;
  #retiredAt: Date | undefined;

  private constructor(snapshot: CookSnapshot, version: number) {
    this.#id = snapshot.id;
    this.#restaurantId = snapshot.restaurantId;
    this.#name = snapshot.name;
    this.#onLine = snapshot.onLine;
    this.#lastAssignedAt = snapshot.lastAssignedAt;
    this.#retiredAt = snapshot.retiredAt;
    this.#version = version;
  }

  /** A hired cook is off line until somebody puts them on line. */
  static hire(input: { id: string; restaurantId: string; name: string }): Cook {
    return new Cook(
      {
        id: input.id,
        restaurantId: input.restaurantId,
        name: cookName(input.name),
        onLine: false,
      },
      0,
    );
  }

  static restore(snapshot: CookSnapshot, version: number): Cook {
    return new Cook(snapshot, version);
  }

  get id(): string {
    return this.#id;
  }

  get restaurantId(): string {
    return this.#restaurantId;
  }

  get name(): string {
    return this.#name;
  }

  get onLine(): boolean {
    return this.#onLine;
  }

  get lastAssignedAt(): Date | undefined {
    return this.#lastAssignedAt;
  }

  get retired(): boolean {
    return this.#retiredAt !== undefined;
  }

  get version(): number {
    return this.#version;
  }

  toSnapshot(): CookSnapshot {
    return {
      id: this.#id,
      restaurantId: this.#restaurantId,
      name: this.#name,
      onLine: this.#onLine,
      ...(this.#lastAssignedAt === undefined
        ? {}
        : { lastAssignedAt: this.#lastAssignedAt }),
      ...(this.#retiredAt === undefined ? {} : { retiredAt: this.#retiredAt }),
    };
  }

  /** Returns whether the line changed; a retired cook cannot return. */
  putOnLine(): boolean {
    if (this.retired) throw new CookRetiredError(this.#id);
    if (this.#onLine) return false;
    this.#onLine = true;
    return true;
  }

  /** Returns whether the line changed; a busy cook stays on line. */
  takeOffLine(input: { holdsOrder: boolean }): boolean {
    if (!this.#onLine) return false;
    if (input.holdsOrder) throw new CookBusyError(this.#id);
    this.#onLine = false;
    return true;
  }

  /** Returns whether the cook left the pool now; only off line. */
  retire(input: { retiredAt: Date }): boolean {
    if (this.retired) return false;
    if (this.#onLine) throw new CookOnLineError(this.#id);
    this.#retiredAt = input.retiredAt;
    return true;
  }

  /** Start Preparation gives the cook an order. */
  assign(input: { assignedAt: Date }): void {
    if (!this.#onLine || this.retired) throw new CookNotOnLineError(this.#id);
    this.#lastAssignedAt = input.assignedAt;
  }
}

/**
 * Repository contract of {@link Cook}. `save` uses optimistic concurrency and
 * returns the committed version.
 */
export interface CookRepository {
  get(id: string): Promise<Cook | undefined>;
  /** Cooks of the restaurant that are not retired, by name, then by id. */
  listPool(restaurantId: string): Promise<readonly Cook[]>;
  save(cook: Cook): Promise<number>;
}

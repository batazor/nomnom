export const InvalidCookReason = {
  EmptyName: "empty_name",
  NameTooLong: "name_too_long",
} as const;

export type InvalidCookReason =
  (typeof InvalidCookReason)[keyof typeof InvalidCookReason];

export class InvalidCookError extends Error {
  readonly code = "invalid_cook";
  readonly reason: InvalidCookReason;

  constructor(reason: InvalidCookReason) {
    super(`invalid cook: ${reason}`);
    this.name = "InvalidCookError";
    this.reason = reason;
  }
}

/** The cook holds an order in Active Preparation and cannot leave the line. */
export class CookBusyError extends Error {
  readonly code = "cook_busy";
  readonly cookId: string;

  constructor(cookId: string) {
    super(`cook ${cookId} holds an order in preparation`);
    this.name = "CookBusyError";
    this.cookId = cookId;
  }
}

/** The cook is on line and cannot be retired. */
export class CookOnLineError extends Error {
  readonly code = "cook_on_line";
  readonly cookId: string;

  constructor(cookId: string) {
    super(`cook ${cookId} is on line`);
    this.name = "CookOnLineError";
    this.cookId = cookId;
  }
}

/** The cook left the Cook Pool and takes no work any more. */
export class CookRetiredError extends Error {
  readonly code = "cook_retired";
  readonly cookId: string;

  constructor(cookId: string) {
    super(`cook ${cookId} is retired`);
    this.name = "CookRetiredError";
    this.cookId = cookId;
  }
}

/** Only a cook on line gets an order. */
export class CookNotOnLineError extends Error {
  readonly code = "cook_not_on_line";
  readonly cookId: string;

  constructor(cookId: string) {
    super(`cook ${cookId} is not on line`);
    this.name = "CookNotOnLineError";
    this.cookId = cookId;
  }
}

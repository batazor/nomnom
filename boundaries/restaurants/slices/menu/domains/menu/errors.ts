export const InvalidMenuReason = {
  EmptyItemName: "empty_item_name",
  DuplicateMenuItem: "duplicate_menu_item",
  UnknownMenuItem: "unknown_menu_item",
} as const;

export type InvalidMenuReason =
  (typeof InvalidMenuReason)[keyof typeof InvalidMenuReason];

export class InvalidMenuError extends Error {
  readonly code = "invalid_menu";
  readonly reason: InvalidMenuReason;

  constructor(reason: InvalidMenuReason) {
    super(`invalid menu: ${reason}`);
    this.name = "InvalidMenuError";
    this.reason = reason;
  }
}

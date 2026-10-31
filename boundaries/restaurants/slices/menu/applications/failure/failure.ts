/**
 * Application errors of the menu slice. Every error exposes a stable `code`
 * that transport adapters map to their own contract.
 */

export class NotFoundError extends Error {
  readonly code = "not_found";
  readonly resource: string;
  readonly resourceId: string;

  constructor(resource: string, resourceId: string) {
    super(`${resource} ${resourceId} was not found`);
    this.name = "NotFoundError";
    this.resource = resource;
    this.resourceId = resourceId;
  }
}

export class AlreadyExistsError extends Error {
  readonly code = "already_exists";
  readonly resource: string;
  readonly resourceId: string;

  constructor(resource: string, resourceId: string) {
    super(`${resource} ${resourceId} already exists`);
    this.name = "AlreadyExistsError";
    this.resource = resource;
    this.resourceId = resourceId;
  }
}

/** Optimistic concurrency conflict: the aggregate version changed after it was loaded. */
export class ConcurrencyConflictError extends Error {
  readonly code = "concurrency_conflict";
  readonly aggregateType: string;
  readonly aggregateId: string;

  constructor(aggregateType: string, aggregateId: string) {
    super(`${aggregateType} ${aggregateId} was modified concurrently`);
    this.name = "ConcurrencyConflictError";
    this.aggregateType = aggregateType;
    this.aggregateId = aggregateId;
  }
}

/** The subject has no permission for the action. */
export class PermissionDeniedError extends Error {
  readonly code = "permission_denied";
  readonly action: string;

  constructor(action: string) {
    super(`permission denied: ${action}`);
    this.name = "PermissionDeniedError";
    this.action = action;
  }
}

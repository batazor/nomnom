/**
 * Application errors of the simulator slice. Every error exposes a stable `code`
 * that transport adapters map to their own contract.
 */

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

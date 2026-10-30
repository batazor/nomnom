/**
 * Authorization errors shared by the use cases of `restaurants`; `code` is the
 * same stable contract as in `pkg/usecase/errors.ts`.
 */

export class PermissionDeniedError extends Error {
  readonly code = "permission_denied";
  readonly action: string;

  constructor(action: string) {
    super(`permission denied: ${action}`);
    this.name = "PermissionDeniedError";
    this.action = action;
  }
}

/**
 * Authorization decision could not be obtained (for example the permission
 * service is unreachable). Access control fails closed with this error.
 */
export class AuthorizationUnavailableError extends Error {
  readonly code = "authorization_unavailable";

  constructor(cause?: unknown) {
    super("authorization decision is unavailable", { cause });
    this.name = "AuthorizationUnavailableError";
  }
}

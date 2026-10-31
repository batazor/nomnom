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

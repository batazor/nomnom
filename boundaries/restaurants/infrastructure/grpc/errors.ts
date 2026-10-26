import { Code, ConnectError } from "@connectrpc/connect";

export function requireField(name: string, value: string): void {
  if (value.trim().length === 0) {
    throw new ConnectError(`${name} is required`, Code.InvalidArgument);
  }
}

/** gRPC status of application and domain errors by their stable `code`. */
const STATUS_BY_ERROR_CODE: Readonly<Record<string, Code>> = {
  not_found: Code.NotFound,
  permission_denied: Code.PermissionDenied,
  authorization_unavailable: Code.Unavailable,
  already_exists: Code.AlreadyExists,
  duplicate_order_id: Code.AlreadyExists,
  concurrency_conflict: Code.Aborted,
  invalid_restaurant: Code.InvalidArgument,
  invalid_menu: Code.InvalidArgument,
  invalid_money: Code.InvalidArgument,
  invalid_restaurant_order: Code.InvalidArgument,
  invalid_quantity: Code.InvalidArgument,
  invalid_page_size: Code.InvalidArgument,
  invalid_market_id: Code.InvalidArgument,
  market_mismatch: Code.InvalidArgument,
  invalid_transition: Code.FailedPrecondition,
  guard_failed: Code.FailedPrecondition,
  already_in_state: Code.FailedPrecondition,
  invalid_cook: Code.InvalidArgument,
  cook_busy: Code.FailedPrecondition,
  cook_on_line: Code.FailedPrecondition,
  cook_retired: Code.FailedPrecondition,
  cook_not_on_line: Code.FailedPrecondition,
  // FAILED_PRECONDITION of StartPreparation is a forbidden transition.
  no_cook_available: Code.ResourceExhausted,
};

export function transportError(error: unknown): ConnectError {
  if (error instanceof ConnectError) return error;
  if (error instanceof Error && "code" in error) {
    const status = STATUS_BY_ERROR_CODE[String(error.code)];
    if (status !== undefined) return new ConnectError(error.message, status);
  }
  // The client sees no details; the cause stays for the server log
  // (errorLoggingInterceptor).
  return new ConnectError(
    "internal error",
    Code.Internal,
    undefined,
    undefined,
    error,
  );
}

import { BinaryWriter, WireType } from "@bufbuild/protobuf/wire";
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
  invalid_time_zone: Code.InvalidArgument,
  invalid_opening_hours: Code.InvalidArgument,
  invalid_scheduled_order_settings: Code.InvalidArgument,
  invalid_order_schedule: Code.InvalidArgument,
  invalid_delivery_window: Code.InvalidArgument,
  // SCHEDULED_ORDERS_UNAVAILABLE (FR-674): no settings or not accepting now.
  scheduled_orders_unavailable: Code.FailedPrecondition,
  invalid_menu: Code.InvalidArgument,
  invalid_money: Code.InvalidArgument,
  invalid_restaurant_order: Code.InvalidArgument,
  invalid_quantity: Code.InvalidArgument,
  invalid_page_size: Code.InvalidArgument,
  invalid_batch: Code.InvalidArgument,
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

/** Domain of the google.rpc.ErrorInfo details of this service. */
export const ERROR_INFO_DOMAIN = "nomnom.restaurants.v1";

/**
 * Errors callers tell apart by `google.rpc.ErrorInfo.reason`, not by the
 * status alone: `orders` recognises the refusal of ReserveScheduledSlot by
 * FAILED_PRECONDITION with reason SCHEDULED_ORDERS_UNAVAILABLE (FR-674).
 */
const ERROR_INFO_REASON_BY_ERROR_CODE: Readonly<Record<string, string>> = {
  scheduled_orders_unavailable: "SCHEDULED_ORDERS_UNAVAILABLE",
};

/**
 * google.rpc.ErrorInfo `{ reason = 1, domain = 2 }` in its wire form; the
 * googleapis types are not a dependency of this module.
 */
const errorInfo = (reason: string) => ({
  type: "google.rpc.ErrorInfo",
  value: new BinaryWriter()
    .tag(1, WireType.LengthDelimited)
    .string(reason)
    .tag(2, WireType.LengthDelimited)
    .string(ERROR_INFO_DOMAIN)
    .finish(),
});

export function transportError(error: unknown): ConnectError {
  if (error instanceof ConnectError) return error;
  if (error instanceof Error && "code" in error) {
    const status = STATUS_BY_ERROR_CODE[String(error.code)];
    if (status !== undefined) {
      const transport = new ConnectError(error.message, status);
      const reason = ERROR_INFO_REASON_BY_ERROR_CODE[String(error.code)];
      if (reason !== undefined) transport.details.push(errorInfo(reason));
      return transport;
    }
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

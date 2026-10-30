import { PermissionDeniedError } from "./errors.ts";

/**
 * Awaits a decision of an access port and throws
 * {@link PermissionDeniedError} for `action` when it denies. A failure to
 * obtain the decision propagates unchanged (fail closed).
 */
export const requirePermission = async (
  allowed: Promise<boolean>,
  action: string,
): Promise<void> => {
  if (!(await allowed)) {
    throw new PermissionDeniedError(action);
  }
};

import type { Subject } from "../../../../pkg/authz/subject.ts";

/**
 * Authorization port of the `menu` slice; the adapter maps it to a permission
 * over `restaurant:<restaurantId>`. Throws when no decision can be obtained.
 */
export interface MenuAccessControl {
  canManageMenu(subject: Subject, restaurantId: string): Promise<boolean>;
}

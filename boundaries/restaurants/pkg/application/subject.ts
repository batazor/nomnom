/**
 * Verified caller identity. Transport adapters build it only from a verified
 * access token or signed internal identity, never from a plain header.
 */
export type Subject = Readonly<{
  type: "user" | "service";
  id: string;
  /**
   * Freshest authorization revision (SpiceDB ZedToken) the caller knows, for
   * example of its own relationship write in the same synchronous scenario.
   * It only makes checks at least as fresh; it never grants a permission.
   */
  authorizationRevision?: string;
  /**
   * Authenticated service that delegated a `user` subject (ADR 32), kept for
   * logs and audit only; authorization never uses it.
   */
  delegate?: string;
}>;

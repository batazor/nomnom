/**
 * Verified caller identity. Transport adapters build it only from a verified
 * access token or signed internal identity, never from a plain header.
 */
export type Subject = Readonly<{
  type: "user" | "service";
  id: string;
}>;

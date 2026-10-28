import type { CombinedError } from "urql";

import { bffErrorCodes } from "../../shared/graphql/errors";

// Codes of the BFF errors the owner screen tells apart; the rest is a
// generic failure.
export type OpeningErrorKind =
  | "notFound"
  | "notPermitted"
  | "unauthenticated"
  | "failed";

const kindByCode: Record<string, OpeningErrorKind> = {
  NotFound: "notFound",
  PermissionDenied: "notPermitted",
  Unauthenticated: "unauthenticated",
};

export function openingErrorKind(error: CombinedError): OpeningErrorKind {
  if (error.networkError) return "failed";
  for (const code of bffErrorCodes(error)) {
    const kind = kindByCode[code];
    if (kind) return kind;
  }
  return "failed";
}

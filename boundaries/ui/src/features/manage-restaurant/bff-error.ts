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

// Interval of a schedule restaurants rejected, as BFF names it in the
// INVALID_ARGUMENT of setOpeningHours (FR-700): "invalid opening hours:
// <reason> at day <day> <opens>-<closes>".
export type InvalidInterval = Readonly<{
  reason: "overlap" | "invalid_time" | "empty_interval" | "invalid_day";
  day: number;
  opens: string;
  closes: string;
}>;

const invalidHours =
  /^invalid opening hours: (overlap|invalid_time|empty_interval|invalid_day) at day (-?\d+) (\S*)-(\S*)$/;

type NestedErrors = { errors?: { message?: unknown }[] };

export function invalidOpeningHours(
  error: CombinedError,
): InvalidInterval | null {
  if (!bffErrorCodes(error).includes("InvalidArgument")) return null;
  for (const graphqlError of error.graphQLErrors) {
    const nested = (graphqlError.extensions as NestedErrors).errors ?? [];
    for (const message of [
      graphqlError.message,
      ...nested.map((inner) => inner.message),
    ]) {
      const match =
        typeof message === "string" ? invalidHours.exec(message) : null;
      if (match) {
        return {
          reason: match[1] as InvalidInterval["reason"],
          day: Number(match[2]),
          opens: match[3],
          closes: match[4],
        };
      }
    }
  }
  return null;
}

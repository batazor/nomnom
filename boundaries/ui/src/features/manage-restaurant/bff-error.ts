import type { CombinedError } from "urql";

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

type ErrorExtensions = {
  code?: unknown;
  errors?: { extensions?: { code?: unknown } }[];
};

// Cosmo Router puts the gRPC code of the plugin under
// errors[].extensions.errors[].extensions.code ("PermissionDenied", …).
export function bffErrorCodes(error: CombinedError): string[] {
  return error.graphQLErrors.flatMap((graphqlError) => {
    const extensions = graphqlError.extensions as ErrorExtensions;
    const nested = (extensions.errors ?? []).map(
      (inner) => inner.extensions?.code,
    );
    return [extensions.code, ...nested].filter(
      (code): code is string => typeof code === "string",
    );
  });
}

export function openingErrorKind(error: CombinedError): OpeningErrorKind {
  if (error.networkError) return "failed";
  for (const code of bffErrorCodes(error)) {
    const kind = kindByCode[code];
    if (kind) return kind;
  }
  return "failed";
}

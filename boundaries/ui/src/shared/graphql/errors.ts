import type { CombinedError } from "urql";

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

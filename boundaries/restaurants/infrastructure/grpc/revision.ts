import type { Subject } from "../../pkg/application/subject.ts";

/** gRPC metadata carrying the caller's authorization revision (docs/security.md). */
export const AUTHORIZATION_REVISION_HEADER = "authorization-revision";
const MAX_REVISION_LENGTH = 1024;

/**
 * Attaches the caller's ZedToken from metadata to the verified subject. An
 * absent or oversized value is ignored: the revision only affects freshness.
 */
export function withRequestRevision(
  subject: Subject,
  context: { requestHeader?: Headers } | undefined,
): Subject {
  const revision = context?.requestHeader
    ?.get(AUTHORIZATION_REVISION_HEADER)
    ?.trim();
  if (!revision || revision.length > MAX_REVISION_LENGTH) {
    return subject;
  }
  return { ...subject, authorizationRevision: revision };
}

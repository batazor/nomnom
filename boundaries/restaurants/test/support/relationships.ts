import { v1 } from "@authzed/authzed-node";

import type { RelationshipsClient } from "../../infrastructure/spicedb/client.ts";

const key = (relationship: v1.Relationship | undefined): string =>
  `${relationship?.resource?.objectType}:${relationship?.resource?.objectId}#${relationship?.relation}@${relationship?.subject?.object?.objectType}:${relationship?.subject?.object?.objectId}`;

const parse = (tuple: string): v1.Relationship => {
  const [, resourceType, resourceId, relation, subjectType, subjectId] =
    /^([^:]+):([^#]+)#([^@]+)@([^:]+):(.+)$/.exec(tuple) ?? [];
  return v1.Relationship.create({
    resource: v1.ObjectReference.create({
      objectType: resourceType,
      objectId: resourceId,
    }),
    relation,
    subject: v1.SubjectReference.create({
      object: v1.ObjectReference.create({
        objectType: subjectType,
        objectId: subjectId,
      }),
    }),
  });
};

/**
 * In-memory relationship graph with SpiceDB paging semantics: results sorted,
 * `optionalLimit` per call and an opaque cursor after the last result.
 */
export class FakeRelationshipsClient implements RelationshipsClient {
  readonly tuples = new Set<string>();
  readonly writes: string[] = [];
  readonly reads: v1.ReadRelationshipsRequest[] = [];
  failWrites: Error | undefined;
  failReads: Error | undefined;
  /** ZedTokens returned by writes: `zed-1`, `zed-2`, … in write order. */
  #revisions = 0;

  constructor(...tuples: string[]) {
    for (const tuple of tuples) this.tuples.add(tuple);
  }

  has(tuple: string): boolean {
    return this.tuples.has(tuple);
  }

  async writeRelationships(
    request: v1.WriteRelationshipsRequest,
  ): Promise<v1.WriteRelationshipsResponse> {
    if (this.failWrites !== undefined) throw this.failWrites;
    for (const update of request.updates) {
      const tuple = key(update.relationship);
      if (update.operation === v1.RelationshipUpdate_Operation.DELETE) {
        this.writes.push(`delete ${tuple}`);
        this.tuples.delete(tuple);
      } else {
        this.writes.push(`touch ${tuple}`);
        this.tuples.add(tuple);
      }
    }
    this.#revisions += 1;
    return v1.WriteRelationshipsResponse.create({
      writtenAt: v1.ZedToken.create({ token: `zed-${this.#revisions}` }),
    });
  }

  async readRelationships(
    request: v1.ReadRelationshipsRequest,
  ): Promise<v1.ReadRelationshipsResponse[]> {
    this.reads.push(request);
    if (this.failReads !== undefined) throw this.failReads;
    const filter = request.relationshipFilter;
    const after = request.optionalCursor?.token ?? "";
    const matching = [...this.tuples]
      .map(parse)
      .filter(
        (relationship) =>
          relationship.resource?.objectType === filter?.resourceType &&
          (filter?.optionalRelation === "" ||
            relationship.relation === filter?.optionalRelation) &&
          (relationship.resource?.objectId ?? "").startsWith(
            filter?.optionalResourceIdPrefix ?? "",
          ),
      )
      .map((relationship) => ({ relationship, token: key(relationship) }))
      .sort((left, right) => (left.token < right.token ? -1 : 1))
      .filter((entry) => after === "" || entry.token > after);
    const limited =
      request.optionalLimit > 0
        ? matching.slice(0, request.optionalLimit)
        : matching;
    return limited.map((entry) =>
      v1.ReadRelationshipsResponse.create({
        relationship: entry.relationship,
        afterResultCursor: v1.Cursor.create({ token: entry.token }),
      }),
    );
  }
}

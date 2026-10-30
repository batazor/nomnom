// Records in the Schema Registry wire format, as the SchemaFraming SMT of the
// connectors writes them (FR-714), and the Registry of a unit test.
import {
  type SchemaSubjects,
  UnknownSchemaError,
} from "../../infrastructure/messaging/schema-registry.ts";

/** Schema id the tests frame their records with. */
export const SCHEMA_ID = 7;

/** `payload` framed by the schema id and the index of a top-level message. */
export const framed = (
  payload: Uint8Array,
  schemaId = SCHEMA_ID,
  messageIndex = 0,
): Uint8Array => {
  const indexes = messageIndex === 0 ? [0] : [2, ...zigzagVarint(messageIndex)];
  const value = new Uint8Array(5 + indexes.length + payload.length);
  new DataView(value.buffer).setUint32(1, schemaId);
  value.set(indexes, 5);
  value.set(payload, 5 + indexes.length);
  return value;
};

const zigzagVarint = (value: number): number[] => {
  const bytes: number[] = [];
  let zigzag = value << 1;
  while (zigzag > 0x7f) {
    bytes.push((zigzag & 0x7f) | 0x80);
    zigzag >>>= 7;
  }
  bytes.push(zigzag);
  return bytes;
};

/** Registry of a unit test: the subjects of each known schema id. */
export class TestSchemas implements SchemaSubjects {
  readonly #subjects: ReadonlyMap<number, readonly string[]>;

  constructor(subjects: Readonly<Record<number, readonly string[]>>) {
    this.#subjects = new Map(
      Object.entries(subjects).map(([id, names]) => [Number(id), names]),
    );
  }

  async subjects(schemaId: number): Promise<readonly string[]> {
    const subjects = this.#subjects.get(schemaId);
    if (subjects === undefined) throw new UnknownSchemaError(schemaId);
    return subjects;
  }
}

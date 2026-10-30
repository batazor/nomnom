// Value записи Integration Event в wire format Schema Registry (FR-714): magic
// byte 0, id схемы (4 байта big-endian), индексы сообщения (массив zigzag
// varint, один байт 0 — первое сообщение файла) и байты Protobuf контракта.
// Его пишет SMT SchemaFraming коннектора Debezium.
import { PermanentMessageError } from "./envelope.ts";

export type WireFrame = Readonly<{ schemaId: number; payload: Uint8Array }>;

const MAGIC_BYTE = 0;

const malformed = (reason: string): PermanentMessageError =>
  new PermanentMessageError(
    `value is not in the schema registry wire format: ${reason}`,
    "deserialization",
  );

/** Zigzag varint, как `binary.Varint` в Go и `ByteUtils.readVarint` Kafka. */
const readVarint = (
  bytes: Uint8Array,
  offset: number,
): { value: number; next: number } => {
  let raw = 0;
  for (
    let shift = 0, at = offset;
    at < bytes.length && shift < 35;
    shift += 7
  ) {
    const byte = bytes[at] as number;
    raw |= (byte & 0x7f) << shift;
    at += 1;
    if ((byte & 0x80) === 0) {
      return { value: (raw >>> 1) ^ -(raw & 1), next: at };
    }
  }
  throw malformed("message indexes are truncated");
};

/** Отделяет id схемы от payload; ошибка — deserialization, повтор её не исправит. */
export function parseWireFrame(value: Uint8Array): WireFrame {
  if (value.length < 6) throw malformed(`${value.length} bytes`);
  if (value[0] !== MAGIC_BYTE) {
    throw malformed(`starts with 0x${(value[0] as number).toString(16)}`);
  }
  const schemaId = new DataView(
    value.buffer,
    value.byteOffset,
    value.byteLength,
  ).getUint32(1);
  let { value: count, next } = readVarint(value, 5);
  if (count < 0) throw malformed("negative message index count");
  while (count > 0) {
    const index = readVarint(value, next);
    if (index.value < 0) throw malformed("negative message index");
    next = index.next;
    count -= 1;
  }
  return Object.freeze({ schemaId, payload: value.subarray(next) });
}

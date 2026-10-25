import {
  type GraphQLOutputType,
  isEnumType,
  isListType,
  isNonNullType,
  isObjectType,
  isScalarType,
} from "graphql";

const scalarChecks: Record<string, (value: unknown) => boolean> = {
  ID: (value) => typeof value === "string",
  String: (value) => typeof value === "string",
  Int: (value) => Number.isInteger(value),
  Float: (value) => typeof value === "number",
  Boolean: (value) => typeof value === "boolean",
};

// Lists every place where a fixture value does not match a schema output type:
// unknown fields, unknown enum values, wrong scalars and missing non-null fields.
export function schemaMismatches(
  value: unknown,
  type: GraphQLOutputType,
  path = "$",
): string[] {
  if (isNonNullType(type)) {
    if (value === null || value === undefined) return [`${path}: required`];
    return schemaMismatches(value, type.ofType, path);
  }
  if (value === null || value === undefined) return [];

  if (isListType(type)) {
    if (!Array.isArray(value)) return [`${path}: expected a list`];
    return value.flatMap((item, index) =>
      schemaMismatches(item, type.ofType, `${path}[${index}]`),
    );
  }
  if (isEnumType(type)) {
    return typeof value === "string" && type.getValue(value)
      ? []
      : [`${path}: ${String(value)} is not a ${type.name} value`];
  }
  if (isScalarType(type)) {
    const check = scalarChecks[type.name];
    return !check || check(value) ? [] : [`${path}: expected ${type.name}`];
  }
  if (isObjectType(type)) {
    if (typeof value !== "object") return [`${path}: expected ${type.name}`];
    const fields = type.getFields();
    const record = value as Record<string, unknown>;
    const unknown = Object.keys(record)
      .filter((key) => !(key in fields))
      .map((key) => `${path}.${key}: not a field of ${type.name}`);
    return [
      ...unknown,
      ...Object.values(fields).flatMap((field) =>
        schemaMismatches(
          record[field.name],
          field.type,
          `${path}.${field.name}`,
        ),
      ),
    ];
  }
  return [`${path}: unsupported type`];
}

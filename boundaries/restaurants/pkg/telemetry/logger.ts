// Structured stdout logs (docs/observability.md, "Логи"): one JSON record per
// line with UTC `timestamp`, `severity`, a short `message`, the service
// identity of the telemetry resource, `trace_id`/`span_id` of the active span
// (absent outside a span), operation fields and error fields with the stack
// as a separate structured field. Values are redacted before they are
// written: secrets by key name, credentials in URLs and bearer tokens in text.
import { isSpanContextValid, trace } from "@opentelemetry/api";

import type { ResourceAttributes } from "./resource.ts";

export type Severity = "DEBUG" | "INFO" | "WARN" | "ERROR";

/** Operation and search fields of a record; `error` takes an Error. */
export type LogFields = Readonly<Record<string, unknown>>;

export type LogMethod = (message: string, fields?: LogFields) => void;

export interface Logger {
  readonly debug: LogMethod;
  readonly info: LogMethod;
  readonly warn: LogMethod;
  readonly error: LogMethod;
  /** Logger whose records carry `fields` as well (e.g. `operation`). */
  child(fields: LogFields): Logger;
}

/** Resource attributes repeated in every record. */
const IDENTITY_FIELDS = [
  "service.namespace",
  "service.name",
  "service.version",
  "service.instance.id",
  "deployment.environment.name",
] as const;

const SEVERITY_ORDER: Readonly<Record<Severity, number>> = {
  DEBUG: 10,
  INFO: 20,
  WARN: 30,
  ERROR: 40,
};

export type LoggerOptions = Readonly<{
  resource: ResourceAttributes;
  /** One serialized record without the trailing newline; stdout by default. */
  write?: (line: string) => void;
  /** Records below it are dropped; INFO by default. */
  level?: Severity;
  now?: () => Date;
}>;

export const REDACTED = "[REDACTED]";

/** Keys whose values never reach a log record. */
const SECRET_KEY =
  /pass(word)?|secret|token|authori[sz]ation|cookie|credential|preshared|api[-_]?key|assertion|private[-_]?key/i;

/** `scheme://user:password@` → `scheme://user:[REDACTED]@`; bearer tokens. */
export function redactText(text: string): string {
  return text
    .replace(
      /\b([a-z][a-z0-9+.-]*:\/\/)([^:@/\s]+):([^@/\s]+)@/gi,
      `$1$2:${REDACTED}@`,
    )
    .replace(/\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]+/gi, `$1 ${REDACTED}`);
}

const MAX_DEPTH = 4;

function redactValue(value: unknown, depth: number): unknown {
  if (typeof value === "string") return redactText(value);
  if (
    value === null ||
    typeof value === "number" ||
    typeof value === "boolean"
  ) {
    return value;
  }
  if (typeof value === "bigint") return value.toString();
  if (value instanceof Date) return value.toISOString();
  if (value instanceof Error) return errorFields(value);
  if (depth >= MAX_DEPTH) return "[Truncated]";
  if (Array.isArray(value)) return value.map((v) => redactValue(v, depth + 1));
  if (typeof value === "object") {
    const result: Record<string, unknown> = {};
    for (const [key, nested] of Object.entries(value)) {
      result[key] = SECRET_KEY.test(key)
        ? REDACTED
        : redactValue(nested, depth + 1);
    }
    return result;
  }
  return undefined;
}

/** Error code (stable `code`) or class name; never the message text. */
export function errorType(error: unknown): string {
  if (error instanceof Error) {
    const code = (error as { code?: unknown }).code;
    if (typeof code === "string" && code !== "") return code;
    return error.name || "Error";
  }
  return typeof error;
}

/** `error.*` fields: type, redacted message and stack frames as an array. */
export function errorFields(error: unknown): Record<string, unknown> {
  if (!(error instanceof Error)) {
    return { "error.type": errorType(error), "error.message": "non-error" };
  }
  const fields: Record<string, unknown> = {
    "error.type": errorType(error),
    "error.message": redactText(error.message),
  };
  const frames = (error.stack ?? "")
    .split("\n")
    .slice(1)
    .map((line) => line.trim())
    .filter((line) => line.startsWith("at "));
  if (frames.length > 0) fields["error.stack"] = frames;
  if (error.cause !== undefined) {
    fields["error.cause"] = errorFields(error.cause);
  }
  return fields;
}

export function createLogger(options: LoggerOptions): Logger {
  const write =
    options.write ?? ((line: string) => process.stdout.write(`${line}\n`));
  const now = options.now ?? (() => new Date());
  const threshold = SEVERITY_ORDER[options.level ?? "INFO"];
  const identity: Record<string, string> = {};
  for (const key of IDENTITY_FIELDS) {
    const value = options.resource[key];
    if (value !== undefined) identity[key] = value;
  }

  const build = (bound: LogFields): Logger => {
    const emit =
      (severity: Severity): LogMethod =>
      (message, fields) => {
        if (SEVERITY_ORDER[severity] < threshold) return;
        const record: Record<string, unknown> = {
          timestamp: now().toISOString(),
          severity,
          message: redactText(message),
          ...identity,
        };
        const span = trace.getActiveSpan()?.spanContext();
        if (span !== undefined && isSpanContextValid(span)) {
          record.trace_id = span.traceId;
          record.span_id = span.spanId;
        }
        for (const [key, value] of Object.entries({ ...bound, ...fields })) {
          if (value === undefined || key in record) continue;
          if (key === "error") {
            Object.assign(record, errorFields(value));
            continue;
          }
          record[key] = SECRET_KEY.test(key) ? REDACTED : redactValue(value, 0);
        }
        try {
          write(JSON.stringify(record));
        } catch {
          // Logging never fails the caller (docs/observability.md).
        }
      };
    return {
      debug: emit("DEBUG"),
      info: emit("INFO"),
      warn: emit("WARN"),
      error: emit("ERROR"),
      child: (fields) => build({ ...bound, ...fields }),
    };
  };
  return build({});
}

/** Logger that drops every record: the default of library code and tests. */
export const silentLogger: Logger = {
  debug: () => {},
  info: () => {},
  warn: () => {},
  error: () => {},
  child: () => silentLogger,
};

/**
 * Uncaught errors (a failed startup included) are written as one ERROR
 * record instead of Node's multi-line stderr dump; the process exits 1.
 */
export function logUncaughtErrors(logger: Logger): void {
  const fail = (message: string) => (error: unknown) => {
    logger.error(message, { operation: "process", error });
    process.exit(1);
  };
  process.on("uncaughtException", fail("process failed"));
  process.on("unhandledRejection", fail("unhandled promise rejection"));
}

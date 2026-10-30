import {
  type Context,
  context,
  type Histogram,
  INVALID_SPAN_CONTEXT,
  type Meter,
  metrics,
  type Span,
  SpanStatusCode,
  trace,
} from "@opentelemetry/api";
import type {
  CommandFailedEvent,
  CommandStartedEvent,
  CommandSucceededEvent,
  MongoClient,
} from "mongodb";

import { SpanKind, TRACER_NAME } from "../../pkg/telemetry/tracing.ts";

// MongoDB client spans and `db.client.operation.duration` (OpenTelemetry
// semantic conventions v1.44.0, Database client) from driver command
// monitoring events: the driver emits them inside the async context of the
// operation, so the span continues the active use case or consumer span. Only
// the command name, collection, database and server address are recorded;
// the command document, filters, documents and replies are never read
// (docs/observability.md, "PostgreSQL и MongoDB").

/** Semconv advice for db.client.operation.duration (seconds). */
const OPERATION_BUCKETS = [0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10];

/** Handshake and topology commands: not application operations. */
const IGNORED_COMMANDS = new Set([
  "hello",
  "ismaster",
  "isMaster",
  "ping",
  "buildInfo",
  "saslStart",
  "saslContinue",
  "authenticate",
  "getnonce",
  "endSessions",
]);

type InFlight = {
  readonly span: Span;
  readonly started: number;
  readonly attributes: Record<string, string | number>;
};

export type DatabaseCommandTelemetryOptions = Readonly<{
  meter?: Meter;
  now?: () => number;
}>;

const key = (event: { requestId: number; connectionId?: unknown }) =>
  `${String(event.connectionId)}#${event.requestId}`;

/** `host:port` (or `[v6]:port`) of the event's server. */
function peer(address: string): { address: string; port?: number } {
  const match = /^(\[[^\]]+\]|[^:]+):(\d+)$/.exec(address);
  return match === null
    ? { address }
    : { address: match[1] ?? address, port: Number(match[2]) };
}

/**
 * Attach with {@link instrument} to a MongoClient created with
 * `monitorCommands: true`.
 */
export class DatabaseCommandTelemetry {
  readonly #inFlight = new Map<string, InFlight>();
  readonly #duration: Histogram;
  readonly #now: () => number;

  constructor(options: DatabaseCommandTelemetryOptions = {}) {
    const meter =
      options.meter ?? metrics.getMeter("nomnom.restaurants.mongodb");
    this.#now = options.now ?? (() => performance.now());
    this.#duration = meter.createHistogram("db.client.operation.duration", {
      description: "Duration of database client operations.",
      unit: "s",
      advice: { explicitBucketBoundaries: OPERATION_BUCKETS },
    });
  }

  instrument(client: Pick<MongoClient, "on">): void {
    client.on("commandStarted", (event) => this.started(event));
    client.on("commandSucceeded", (event) => this.succeeded(event));
    client.on("commandFailed", (event) => this.failed(event));
  }

  started(
    event: Pick<
      CommandStartedEvent,
      | "commandName"
      | "databaseName"
      | "address"
      | "requestId"
      | "connectionId"
      | "command"
    >,
    parent: Context = context.active(),
  ): void {
    if (IGNORED_COMMANDS.has(event.commandName)) return;
    // The first field of a CRUD command holds the collection name; any other
    // value (a cursor id, 1) is not a collection.
    const target = (event.command as Record<string, unknown> | undefined)?.[
      event.commandName
    ];
    const collection =
      typeof target === "string"
        ? target
        : typeof (event.command as { collection?: unknown })?.collection ===
            "string"
          ? String((event.command as { collection: string }).collection)
          : undefined;
    const server = peer(event.address);
    const attributes: Record<string, string | number> = {
      "db.system.name": "mongodb",
      "db.operation.name": event.commandName,
      ...(collection === undefined ? {} : { "db.collection.name": collection }),
    };
    // Spans only inside an existing trace: background loops (reconciler) would
    // otherwise open a root trace per command. The metric is still
    // recorded through the non-recording span.
    const parentSpan = trace.getSpanContext(parent);
    const span =
      parentSpan !== undefined && trace.isSpanContextValid(parentSpan)
        ? trace.getTracer(TRACER_NAME).startSpan(
            collection === undefined
              ? event.commandName
              : `${event.commandName} ${collection}`,
            {
              kind: SpanKind.CLIENT,
              attributes: {
                ...attributes,
                "db.namespace": event.databaseName,
                "server.address": server.address,
                ...(server.port === undefined
                  ? {}
                  : { "server.port": server.port }),
              },
            },
            parent,
          )
        : trace.wrapSpanContext(INVALID_SPAN_CONTEXT);
    this.#inFlight.set(key(event), {
      span,
      started: this.#now(),
      attributes,
    });
  }

  succeeded(
    event: Pick<CommandSucceededEvent, "requestId" | "connectionId">,
  ): void {
    this.#finish(event, undefined);
  }

  failed(
    event: Pick<CommandFailedEvent, "requestId" | "connectionId" | "failure">,
  ): void {
    const failure = event.failure as { codeName?: unknown; name?: unknown };
    const type =
      typeof failure?.codeName === "string" && failure.codeName !== ""
        ? failure.codeName
        : typeof failure?.name === "string"
          ? failure.name
          : "MongoError";
    this.#finish(event, type);
  }

  #finish(
    event: { requestId: number; connectionId?: unknown },
    errorType: string | undefined,
  ): void {
    const id = key(event);
    const call = this.#inFlight.get(id);
    if (call === undefined) return;
    this.#inFlight.delete(id);
    if (errorType !== undefined) {
      call.span.setAttribute("error.type", errorType);
      call.span.setStatus({ code: SpanStatusCode.ERROR, message: errorType });
    }
    call.span.end();
    this.#duration.record((this.#now() - call.started) / 1000, {
      ...call.attributes,
      ...(errorType === undefined ? {} : { "error.type": errorType }),
    });
  }
}

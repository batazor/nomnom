// Use case telemetry from the outside (docs/observability.md, "Use cases"),
// the TypeScript counterpart of orders pkg/telemetry/usecase.go: the
// composition root decorates `execute` of an application handler with one
// span `usecase <name>` and one `nomnom.usecase.duration` measurement per
// call (ops/semconv/model/usecase.yaml). Use cases and domains do not import
// OpenTelemetry. Attributes: name, kind, outcome and `error.type` (only on
// failure) from a closed set; never IDs, subjects, payload or error text.
import {
  type Histogram,
  metrics,
  SpanStatusCode,
  trace,
} from "@opentelemetry/api";

export const USECASE_INSTRUMENTATION = "nomnom.restaurants.usecase";

/** Seconds, the same boundaries as orders. */
export const USECASE_DURATION_BUCKETS = [
  0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30,
];

export type UseCaseKind = "command" | "query";

/** Stable snake_case name, e.g. `accept_order`, and its kind. */
export type UseCase = Readonly<{ name: string; kind: UseCaseKind }>;

export const command = (name: string): UseCase => ({ name, kind: "command" });
export const query = (name: string): UseCase => ({ name, kind: "query" });

export type Outcome = "success" | "rejected" | "error";

export type Failure = Readonly<{
  outcome: Exclude<Outcome, "success">;
  errorType: string;
}>;

/** Maps a use case error to a {@link Failure}; `undefined` — unknown. */
export type Classifier = (error: unknown) => Failure | undefined;

/**
 * Stable `code` of the application and domain errors of restaurants: an
 * expected refusal by the rules of the use case is `rejected`; a version
 * conflict (retry) and an unavailable authorization backend are `error`.
 */
const REJECTED_CODES = new Set([
  "not_found",
  "permission_denied",
  "already_exists",
  "duplicate_order_id",
  "invalid_restaurant",
  "invalid_menu",
  "invalid_money",
  "invalid_restaurant_order",
  "invalid_quantity",
  "invalid_page_size",
  "invalid_batch",
  "invalid_transition",
  "guard_failed",
  "already_in_state",
]);

const ERROR_CODES = new Set([
  "concurrency_conflict",
  "authorization_unavailable",
  "outbox_outside_unit_of_work",
]);

/** Classifier of the shared error codes of restaurants. */
export const classifyByCode: Classifier = (error) => {
  const code =
    error instanceof Error ? (error as { code?: unknown }).code : undefined;
  if (typeof code !== "string") return undefined;
  if (REJECTED_CODES.has(code)) return { outcome: "rejected", errorType: code };
  if (ERROR_CODES.has(code)) return { outcome: "error", errorType: code };
  return undefined;
};

/** Cancellation is classified before the slice and is always `error`. */
function failure(error: unknown, classify: Classifier): Failure {
  if (error instanceof Error) {
    if (error.name === "AbortError") {
      return { outcome: "error", errorType: "canceled" };
    }
    if (error.name === "TimeoutError") {
      return { outcome: "error", errorType: "deadline_exceeded" };
    }
  }
  return classify(error) ?? { outcome: "error", errorType: "internal" };
}

/**
 * Instruments shared by the decorated handlers of a process. Created after
 * startTelemetry (in the container), so the histogram lives on the
 * registered MeterProvider; before that it is a no-op.
 */
export class UseCaseTelemetry {
  readonly #duration: Histogram;
  readonly #classify: Classifier;

  constructor(options: { classify?: Classifier } = {}) {
    this.#classify = options.classify ?? classifyByCode;
    this.#duration = metrics
      .getMeter(USECASE_INSTRUMENTATION)
      .createHistogram("nomnom.usecase.duration", {
        description: "Duration of one application use case call.",
        unit: "s",
        advice: { explicitBucketBoundaries: USECASE_DURATION_BUCKETS },
      });
  }

  /**
   * Replaces `handler.execute` with an instrumented call and returns the
   * same handler, so its type in the container does not change. The result
   * and the error are passed through unchanged.
   */
  instrument<H extends { execute(input: never): Promise<unknown> }>(
    useCase: UseCase,
    handler: H,
  ): H {
    const execute = handler.execute.bind(handler) as (
      input: unknown,
    ) => Promise<unknown>;
    const attributes = {
      "nomnom.usecase.name": useCase.name,
      "nomnom.usecase.kind": useCase.kind,
    };
    const tracer = trace.getTracer(USECASE_INSTRUMENTATION);
    const instrumented = (input: unknown): Promise<unknown> =>
      tracer.startActiveSpan(
        `usecase ${useCase.name}`,
        { attributes },
        async (span) => {
          const started = performance.now();
          let outcome: Readonly<{ outcome: Outcome; errorType?: string }> = {
            outcome: "success",
          };
          try {
            return await execute(input);
          } catch (error) {
            outcome = failure(error, this.#classify);
            throw error;
          } finally {
            const result = {
              ...attributes,
              "nomnom.outcome": outcome.outcome,
              ...(outcome.errorType === undefined
                ? {}
                : { "error.type": outcome.errorType }),
            };
            span.setAttributes(result);
            if (outcome.outcome === "error") {
              span.setStatus({
                code: SpanStatusCode.ERROR,
                message: outcome.errorType ?? "internal",
              });
            }
            span.end();
            this.#duration.record((performance.now() - started) / 1000, result);
          }
        },
      );
    Object.defineProperty(handler, "execute", {
      value: instrumented,
      configurable: true,
      writable: true,
    });
    return handler;
  }
}

// Command restaurants-admin — operator actions of restaurants, run once in the
// restaurants-consumer pod (kubectl exec), which already has the Kafka
// configuration:
//
//   restaurants-admin dlq list --stream <topic>
//   restaurants-admin dlq redrive --stream <topic> [--partition <p> --through <offset>]
//   restaurants-admin dlq discard --stream <topic> --partition <p> --through <offset> --reason <text>
//
// DLQ — dead letters of the consumer group of restaurants-consumer (ADR 28,
// runbook docs/dlq.md), the same contract as orders-admin. `dlq list` writes
// one JSON line per dead letter to stdout; logs go to stderr.
import { parseArgs } from "node:util";

import {
  loadConsumerConfig,
  loadKafkaConfig,
  loadLocalEnv,
} from "../../pkg/config/config.ts";
import {
  type DeadLetter,
  KafkaDeadLetters,
  REDRIVEN_FROM_HEADER,
  type Through,
} from "../../pkg/infrastructure/kafka/dead-letters.ts";
import { KafkaPublisher } from "../../pkg/infrastructure/kafka/publisher.ts";
import { DEAD_LETTER_HEADERS } from "../../pkg/infrastructure/messaging/processor.ts";
import { createLogger, logUncaughtErrors } from "../../pkg/telemetry/logger.ts";
import { telemetryResource } from "../../pkg/telemetry/resource.ts";
import { DELIVERY_EVENTS_TOPIC } from "../../slices/restaurantorder/infrastructure/integration/delivery-picked-up.ts";

const SERVICE_NAME = "restaurants-admin";

/** Topics restaurants-consumer dead-letters; only their DLQs are its own. */
const STREAMS: readonly string[] = [DELIVERY_EVENTS_TOPIC];

const USAGE = `usage:
  restaurants-admin dlq list --stream <topic>
  restaurants-admin dlq redrive --stream <topic> [--partition <p> --through <offset>]
  restaurants-admin dlq discard --stream <topic> --partition <p> --through <offset> --reason <text>`;

loadLocalEnv();
const logger = createLogger({
  resource: telemetryResource(SERVICE_NAME),
  write: (line) => process.stderr.write(`${line}\n`),
});
logUncaughtErrors(logger);

const abort = new AbortController();
process.once("SIGINT", () => abort.abort());
process.once("SIGTERM", () => abort.abort());

try {
  await run(process.argv.slice(2), abort.signal);
} catch (error) {
  logger.error("restaurants-admin failed", { operation: "dlq", error });
  process.exitCode = 1;
}

async function run(args: string[], signal: AbortSignal): Promise<void> {
  const [command, action, ...rest] = args;
  if (command !== "dlq" || action === undefined) throw new Error(USAGE);
  await deadLetters(action, rest, signal);
}

async function deadLetters(
  action: string,
  args: string[],
  signal: AbortSignal,
): Promise<void> {
  const { values } = parseArgs({
    args,
    options: {
      stream: { type: "string" },
      partition: { type: "string" },
      through: { type: "string" },
      reason: { type: "string" },
    },
    strict: true,
  });
  const stream = values.stream ?? "";
  if (!STREAMS.includes(stream)) {
    throw new Error(
      `--stream must be one of ${STREAMS.join(", ")}, got "${stream}"`,
    );
  }
  const bound = throughFlag(values.partition, values.through);

  const kafkaConfig = loadKafkaConfig(process.env);
  if (!kafkaConfig.enabled) {
    throw new Error(
      "KAFKA__BROKERS is required: restaurants-admin reads the DLQ from Kafka",
    );
  }
  const { groupId } = loadConsumerConfig(process.env);
  const log = logger.child({
    operation: "dlq",
    stream,
    "messaging.consumer.group.name": groupId,
  });

  const publisher = await KafkaPublisher.connect(kafkaConfig);
  try {
    const letters = await KafkaDeadLetters.connect(
      kafkaConfig,
      groupId,
      publisher,
    );
    try {
      switch (action) {
        case "list": {
          if (bound !== undefined) {
            throw new Error("dlq list takes no --partition and --through");
          }
          for (const letter of await letters.list(stream, signal)) {
            process.stdout.write(`${JSON.stringify(describe(letter))}\n`);
          }
          return;
        }
        case "redrive": {
          const redriven = await letters.redrive(stream, bound, signal);
          log.info("dead letters redriven", { count: redriven });
          return;
        }
        case "discard": {
          const reason = values.reason?.trim() ?? "";
          if (bound === undefined || reason === "") {
            throw new Error(
              "dlq discard requires --partition, --through and --reason",
            );
          }
          const discarded = await letters.discard(
            stream,
            bound,
            (letter) =>
              log.warn("dead letter discarded", {
                dead_letter: describe(letter),
                discard_reason: reason,
              }),
            signal,
          );
          log.info("dead letters discarded", { count: discarded });
          return;
        }
        default:
          throw new Error(`unknown dlq action "${action}"\n${USAGE}`);
      }
    } finally {
      await letters.close();
    }
  } finally {
    await publisher.close();
  }
}

/** Bound of `--partition` and `--through`: both or neither. */
function throughFlag(
  partition: string | undefined,
  through: string | undefined,
): Through | undefined {
  if (partition === undefined && through === undefined) return undefined;
  if (partition === undefined || through === undefined) {
    throw new Error("--partition and --through go together");
  }
  return {
    partition: nonNegative("--partition", partition),
    offset: nonNegative("--through", through),
  };
}

function nonNegative(flag: string, raw: string): number {
  const value = Number(raw);
  if (!/^\d+$/.test(raw) || !Number.isSafeInteger(value)) {
    throw new Error(`${flag} must be a non-negative integer, got "${raw}"`);
  }
  return value;
}

/** One line of `dlq list`. */
function describe(letter: DeadLetter): Record<string, unknown> {
  const redrivenFrom = letter.headers[REDRIVEN_FROM_HEADER];
  return {
    partition: letter.partition,
    offset: letter.offset,
    dead_lettered_at: letter.deadLetteredAt.toISOString(),
    event_id: letter.headers.event_id,
    event_type: letter.headers.event_type,
    aggregate_id: letter.headers.aggregate_id,
    occurred_at: letter.headers.occurred_at,
    reason: letter.headers[DEAD_LETTER_HEADERS.reason],
    ...(redrivenFrom === undefined ? {} : { redriven_from: redrivenFrom }),
  };
}

//! Операторские действия `delivery`, запускается разово в pod `delivery`
//! (`kubectl exec`) и читает его файл `CONFIG_FILE` (секции `spec.kafka` и
//! `spec.schemaRegistry`); сверяются только `apiVersion` и `kind`, не роль:
//!
//! ```text
//! delivery-admin dlq list --stream <topic>
//! delivery-admin dlq redrive --stream <topic> [--partition <p> --through <offset>]
//! delivery-admin dlq discard --stream <topic> --partition <p> --through <offset> --reason <text>
//! ```
//!
//! DLQ — dead letters consumer group процесса (ADR 28, runbook docs/dlq.md).

use std::process::ExitCode;

use delivery::infrastructure::messaging::{DeadLetter, DeadLetters, REDRIVEN_FROM, Through};
use delivery::infrastructure::repository::OUTBOX_TOPIC;
use delivery::pkg::config;
use delivery::pkg::telemetry::log;
use serde::Serialize;

const SERVICE_NAME: &str = "delivery-admin";

/// Топики, которые читает consumer процесса; только их DLQ — свои.
const STREAMS: [&str; 1] = [OUTBOX_TOPIC];

const USAGE: &str = "usage:
  delivery-admin dlq list --stream <topic>
  delivery-admin dlq redrive --stream <topic> [--partition <p> --through <offset>]
  delivery-admin dlq discard --stream <topic> --partition <p> --through <offset> --reason <text>";

#[derive(Debug, Eq, PartialEq)]
enum Command {
    List {
        stream: String,
    },
    Redrive {
        stream: String,
        through: Option<Through>,
    },
    Discard {
        stream: String,
        through: Through,
        reason: String,
    },
}

/// Одна строка `dlq list`.
#[derive(Serialize)]
struct DeadLetterView<'a> {
    partition: i32,
    offset: i64,
    dead_lettered_at: String,
    event_id: &'a str,
    event_type: &'a str,
    aggregate_id: &'a str,
    occurred_at: &'a str,
    reason: &'a str,
    #[serde(skip_serializing_if = "str::is_empty")]
    redriven_from: &'a str,
}

fn describe(letter: &DeadLetter) -> String {
    let view = DeadLetterView {
        partition: letter.partition,
        offset: letter.offset,
        dead_lettered_at: log::rfc3339(letter.dead_lettered_at),
        event_id: letter.header("event_id"),
        event_type: letter.header("event_type"),
        aggregate_id: letter.header("aggregate_id"),
        occurred_at: letter.header("occurred_at"),
        reason: letter.reason(),
        redriven_from: letter.header(REDRIVEN_FROM),
    };
    serde_json::to_string(&view).unwrap_or_else(|error| format!("{{\"error\":\"{error}\"}}"))
}

#[tokio::main]
async fn main() -> ExitCode {
    log::init_logs(SERVICE_NAME);
    match run(std::env::args().skip(1).collect()).await {
        Ok(()) => ExitCode::SUCCESS,
        Err(error) => {
            tracing::error!(operation = "dlq", error.message = %error, "delivery-admin failed");
            ExitCode::FAILURE
        }
    }
}

async fn run(args: Vec<String>) -> Result<(), String> {
    let command = parse(&args)?;
    let kafka = config::load_kafka().map_err(|error| format!("load kafka config: {error}"))?;
    let letters = DeadLetters::new(&kafka)?;
    let group = kafka.consumer_group.as_str();
    match command {
        Command::List { stream } => {
            for letter in letters.list(&stream).await? {
                println!("{}", describe(&letter));
            }
        }
        Command::Redrive { stream, through } => {
            let count = letters.redrive(&stream, through).await?;
            tracing::info!(
                stream,
                messaging.consumer.group.name = group,
                count,
                "dead letters redriven"
            );
        }
        Command::Discard {
            stream,
            through,
            reason,
        } => {
            let count = letters
                .discard(&stream, through, |letter| {
                    tracing::warn!(
                        stream,
                        messaging.consumer.group.name = group,
                        dead_letter = %describe(letter),
                        discard_reason = %reason,
                        "dead letter discarded"
                    );
                })
                .await?;
            tracing::info!(
                stream,
                messaging.consumer.group.name = group,
                count,
                "dead letters discarded"
            );
        }
    }
    Ok(())
}

fn parse(args: &[String]) -> Result<Command, String> {
    let [scope, action, flags @ ..] = args else {
        return Err(USAGE.to_owned());
    };
    if scope != "dlq" {
        return Err(format!("unknown command {scope:?}\n{USAGE}"));
    }
    let (mut stream, mut partition, mut offset, mut reason) = (None, None, None, None);
    let mut rest = flags.iter();
    while let Some(flag) = rest.next() {
        let value = rest
            .next()
            .ok_or_else(|| format!("{flag} requires a value\n{USAGE}"))?;
        match flag.as_str() {
            "--stream" => stream = Some(value.clone()),
            "--partition" => {
                let parsed = value.parse::<i32>().ok().filter(|parsed| *parsed >= 0);
                partition = Some(parsed.ok_or_else(|| format!("invalid --partition {value:?}"))?);
            }
            "--through" => {
                let parsed = value.parse::<i64>().ok().filter(|parsed| *parsed >= 0);
                offset = Some(parsed.ok_or_else(|| format!("invalid --through {value:?}"))?);
            }
            "--reason" => reason = Some(value.clone()),
            _ => return Err(format!("unknown flag {flag:?}\n{USAGE}")),
        }
    }
    let stream = stream
        .filter(|stream| STREAMS.contains(&stream.as_str()))
        .ok_or_else(|| format!("--stream must be one of {STREAMS:?}"))?;
    let through = match (partition, offset) {
        (None, None) => None,
        (Some(partition), Some(offset)) => Some(Through { partition, offset }),
        _ => return Err("--partition and --through go together".to_owned()),
    };
    if action != "discard" && reason.is_some() {
        return Err("only dlq discard takes --reason".to_owned());
    }
    match action.as_str() {
        "list" if through.is_some() => {
            Err("dlq list takes no --partition and --through".to_owned())
        }
        "list" => Ok(Command::List { stream }),
        "redrive" => Ok(Command::Redrive { stream, through }),
        "discard" => match (through, reason.filter(|reason| !reason.trim().is_empty())) {
            (Some(through), Some(reason)) => Ok(Command::Discard {
                stream,
                through,
                reason,
            }),
            _ => Err("dlq discard requires --partition, --through and --reason".to_owned()),
        },
        _ => Err(format!("unknown dlq action {action:?}\n{USAGE}")),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn parsed(args: &[&str]) -> Result<Command, String> {
        let args: Vec<String> = args.iter().map(|arg| (*arg).to_owned()).collect();
        parse(&args)
    }

    #[test]
    fn parses_the_commands_of_the_runbook() {
        assert_eq!(
            parsed(&["dlq", "list", "--stream", OUTBOX_TOPIC]),
            Ok(Command::List {
                stream: OUTBOX_TOPIC.to_owned()
            })
        );
        assert_eq!(
            parsed(&["dlq", "redrive", "--stream", OUTBOX_TOPIC]),
            Ok(Command::Redrive {
                stream: OUTBOX_TOPIC.to_owned(),
                through: None
            })
        );
        assert_eq!(
            parsed(&[
                "dlq",
                "discard",
                "--stream",
                OUTBOX_TOPIC,
                "--partition",
                "0",
                "--through",
                "41",
                "--reason",
                "INC-12"
            ]),
            Ok(Command::Discard {
                stream: OUTBOX_TOPIC.to_owned(),
                through: Through {
                    partition: 0,
                    offset: 41
                },
                reason: "INC-12".to_owned()
            })
        );
    }

    #[test]
    fn refuses_what_the_contract_does_not_allow() {
        for args in [
            &["dlq"][..],
            &["authz", "resync"],
            &["dlq", "list"],
            &["dlq", "list", "--stream", "orders.v1.order-events"],
            &[
                "dlq",
                "list",
                "--stream",
                OUTBOX_TOPIC,
                "--partition",
                "0",
                "--through",
                "1",
            ],
            &[
                "dlq",
                "redrive",
                "--stream",
                OUTBOX_TOPIC,
                "--partition",
                "0",
            ],
            &["dlq", "redrive", "--stream", OUTBOX_TOPIC, "--reason", "x"],
            &[
                "dlq",
                "redrive",
                "--stream",
                OUTBOX_TOPIC,
                "--through",
                "-1",
                "--partition",
                "0",
            ],
            &["dlq", "discard", "--stream", OUTBOX_TOPIC, "--reason", "x"],
            &[
                "dlq",
                "discard",
                "--stream",
                OUTBOX_TOPIC,
                "--partition",
                "0",
                "--through",
                "1",
            ],
            &["dlq", "drop", "--stream", OUTBOX_TOPIC],
            &["dlq", "list", "--stream"],
        ] {
            assert!(parsed(args).is_err(), "{args:?}");
        }
    }
}

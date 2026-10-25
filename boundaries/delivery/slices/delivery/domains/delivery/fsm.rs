use std::error::Error;
use std::fmt::{Display, Formatter};

use crate::slices::courier::domains::courier::CourierId;

#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
pub enum State {
    Waiting,
    Assigned,
    PickedUp,
    Delivered,
    Failed,
    /// Terminal state of a Delivery Cancellation before Pickup.
    Cancelled,
}

#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
pub enum Trigger {
    Assign,
    Unassign,
    Pickup,
    Complete,
    Fail,
    Cancel,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct TransitionResult {
    pub from: State,
    pub trigger: Trigger,
    pub to: State,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum TransitionError {
    InvalidTransition { from: State, trigger: Trigger },
    GuardFailed { reason: &'static str },
    AlreadyInState { state: State },
}

impl Display for TransitionError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::InvalidTransition { from, trigger } => {
                write!(
                    formatter,
                    "invalid delivery transition from {from:?} with {trigger:?}"
                )
            }
            Self::GuardFailed { reason } => write!(formatter, "delivery guard failed: {reason}"),
            Self::AlreadyInState { state } => {
                write!(formatter, "delivery is already in state {state:?}")
            }
        }
    }
}

impl Error for TransitionError {}

#[derive(Clone, Copy)]
enum Outcome {
    Allowed(State),
    Invalid,
    AlreadyApplied,
}

#[derive(Clone, Copy)]
enum Guard {
    CourierRequired,
}

#[derive(Clone, Copy)]
struct TransitionDefinition {
    from: State,
    trigger: Trigger,
    outcome: Outcome,
    guard: Option<Guard>,
}

const TRANSITION_TABLE: [TransitionDefinition; 36] = [
    definition(
        State::Waiting,
        Trigger::Assign,
        Outcome::Allowed(State::Assigned),
        Some(Guard::CourierRequired),
    ),
    definition(State::Waiting, Trigger::Unassign, Outcome::Invalid, None),
    definition(State::Waiting, Trigger::Pickup, Outcome::Invalid, None),
    definition(State::Waiting, Trigger::Complete, Outcome::Invalid, None),
    definition(State::Waiting, Trigger::Fail, Outcome::Invalid, None),
    definition(
        State::Waiting,
        Trigger::Cancel,
        Outcome::Allowed(State::Cancelled),
        None,
    ),
    definition(
        State::Assigned,
        Trigger::Assign,
        Outcome::AlreadyApplied,
        None,
    ),
    definition(
        State::Assigned,
        Trigger::Unassign,
        Outcome::Allowed(State::Waiting),
        None,
    ),
    definition(
        State::Assigned,
        Trigger::Pickup,
        Outcome::Allowed(State::PickedUp),
        None,
    ),
    definition(State::Assigned, Trigger::Complete, Outcome::Invalid, None),
    definition(
        State::Assigned,
        Trigger::Fail,
        Outcome::Allowed(State::Failed),
        None,
    ),
    definition(
        State::Assigned,
        Trigger::Cancel,
        Outcome::Allowed(State::Cancelled),
        None,
    ),
    definition(State::PickedUp, Trigger::Assign, Outcome::Invalid, None),
    definition(State::PickedUp, Trigger::Unassign, Outcome::Invalid, None),
    definition(
        State::PickedUp,
        Trigger::Pickup,
        Outcome::AlreadyApplied,
        None,
    ),
    definition(
        State::PickedUp,
        Trigger::Complete,
        Outcome::Allowed(State::Delivered),
        None,
    ),
    definition(
        State::PickedUp,
        Trigger::Fail,
        Outcome::Allowed(State::Failed),
        None,
    ),
    definition(State::PickedUp, Trigger::Cancel, Outcome::Invalid, None),
    definition(State::Delivered, Trigger::Assign, Outcome::Invalid, None),
    definition(State::Delivered, Trigger::Unassign, Outcome::Invalid, None),
    definition(State::Delivered, Trigger::Pickup, Outcome::Invalid, None),
    definition(
        State::Delivered,
        Trigger::Complete,
        Outcome::AlreadyApplied,
        None,
    ),
    definition(State::Delivered, Trigger::Fail, Outcome::Invalid, None),
    definition(State::Delivered, Trigger::Cancel, Outcome::Invalid, None),
    definition(State::Failed, Trigger::Assign, Outcome::Invalid, None),
    definition(State::Failed, Trigger::Unassign, Outcome::Invalid, None),
    definition(State::Failed, Trigger::Pickup, Outcome::Invalid, None),
    definition(State::Failed, Trigger::Complete, Outcome::Invalid, None),
    definition(State::Failed, Trigger::Fail, Outcome::AlreadyApplied, None),
    definition(State::Failed, Trigger::Cancel, Outcome::Invalid, None),
    definition(State::Cancelled, Trigger::Assign, Outcome::Invalid, None),
    definition(State::Cancelled, Trigger::Unassign, Outcome::Invalid, None),
    definition(State::Cancelled, Trigger::Pickup, Outcome::Invalid, None),
    definition(State::Cancelled, Trigger::Complete, Outcome::Invalid, None),
    definition(State::Cancelled, Trigger::Fail, Outcome::Invalid, None),
    definition(
        State::Cancelled,
        Trigger::Cancel,
        Outcome::AlreadyApplied,
        None,
    ),
];

const fn definition(
    from: State,
    trigger: Trigger,
    outcome: Outcome,
    guard: Option<Guard>,
) -> TransitionDefinition {
    TransitionDefinition {
        from,
        trigger,
        outcome,
        guard,
    }
}

/// Finds the table outcome for `(from, trigger)` without evaluating guards.
fn resolve(
    from: State,
    trigger: Trigger,
) -> Result<(TransitionResult, Option<Guard>), TransitionError> {
    let Some(rule) = TRANSITION_TABLE
        .iter()
        .find(|rule| rule.from == from && rule.trigger == trigger)
    else {
        return Err(TransitionError::InvalidTransition { from, trigger });
    };

    match rule.outcome {
        Outcome::Invalid => Err(TransitionError::InvalidTransition { from, trigger }),
        Outcome::AlreadyApplied => Err(TransitionError::AlreadyInState { state: from }),
        Outcome::Allowed(to) => Ok((TransitionResult { from, trigger, to }, rule.guard)),
    }
}

/// Checks the table outcome for `(from, trigger)` without evaluating guards.
pub(super) fn ensure_allowed(from: State, trigger: Trigger) -> Result<(), TransitionError> {
    resolve(from, trigger).map(|_| ())
}

pub(super) fn transition(
    from: State,
    trigger: Trigger,
    courier_id: Option<&CourierId>,
) -> Result<TransitionResult, TransitionError> {
    let (result, guard) = resolve(from, trigger)?;
    if matches!(guard, Some(Guard::CourierRequired)) && courier_id.is_none() {
        return Err(TransitionError::GuardFailed {
            reason: "courier_id_required",
        });
    }
    Ok(result)
}

#[cfg(test)]
mod tests {
    use super::*;

    const ALL_STATES: [State; 6] = [
        State::Waiting,
        State::Assigned,
        State::PickedUp,
        State::Delivered,
        State::Failed,
        State::Cancelled,
    ];

    const ALL_TRIGGERS: [Trigger; 6] = [
        Trigger::Assign,
        Trigger::Unassign,
        Trigger::Pickup,
        Trigger::Complete,
        Trigger::Fail,
        Trigger::Cancel,
    ];

    #[test]
    fn transition_table_is_complete() {
        for state in ALL_STATES {
            for trigger in ALL_TRIGGERS {
                assert!(
                    TRANSITION_TABLE
                        .iter()
                        .any(|rule| rule.from == state && rule.trigger == trigger),
                    "missing outcome for {state:?} x {trigger:?}"
                );
            }
        }
        assert_eq!(
            TRANSITION_TABLE.len(),
            ALL_STATES.len() * ALL_TRIGGERS.len()
        );
    }

    #[test]
    fn allowed_transitions_follow_the_table() {
        let courier = CourierId::new("courier-1");
        let cases = [
            (State::Waiting, Trigger::Assign, State::Assigned),
            (State::Assigned, Trigger::Unassign, State::Waiting),
            (State::Assigned, Trigger::Pickup, State::PickedUp),
            (State::Assigned, Trigger::Fail, State::Failed),
            (State::PickedUp, Trigger::Complete, State::Delivered),
            (State::PickedUp, Trigger::Fail, State::Failed),
            (State::Waiting, Trigger::Cancel, State::Cancelled),
            (State::Assigned, Trigger::Cancel, State::Cancelled),
        ];
        for (from, trigger, to) in cases {
            assert_eq!(
                transition(from, trigger, Some(&courier)),
                Ok(TransitionResult { from, trigger, to })
            );
        }
    }

    #[test]
    fn terminal_states_reject_every_other_trigger() {
        for (state, repeated) in [
            (State::Delivered, Trigger::Complete),
            (State::Failed, Trigger::Fail),
            (State::Cancelled, Trigger::Cancel),
        ] {
            for trigger in ALL_TRIGGERS {
                let expected = if trigger == repeated {
                    TransitionError::AlreadyInState { state }
                } else {
                    TransitionError::InvalidTransition {
                        from: state,
                        trigger,
                    }
                };
                assert_eq!(
                    transition(state, trigger, Some(&CourierId::new("courier-1"))),
                    Err(expected)
                );
            }
        }
    }

    #[test]
    fn cancellation_is_rejected_after_pickup() {
        for state in [State::PickedUp, State::Delivered, State::Failed] {
            assert_eq!(
                transition(state, Trigger::Cancel, Some(&CourierId::new("courier-1"))),
                Err(TransitionError::InvalidTransition {
                    from: state,
                    trigger: Trigger::Cancel,
                })
            );
        }
    }

    #[test]
    fn assignment_guard_requires_courier_id() {
        assert_eq!(
            transition(State::Waiting, Trigger::Assign, None).expect_err("missing courier"),
            TransitionError::GuardFailed {
                reason: "courier_id_required"
            }
        );
    }
}

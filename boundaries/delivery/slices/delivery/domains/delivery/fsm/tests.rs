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
fn stored_state_changes_read_as_the_allowed_transition() {
    for from in ALL_STATES {
        for to in ALL_STATES {
            let expected = ALL_TRIGGERS
                .into_iter()
                .find(|trigger| {
                    transition(from, *trigger, Some(&CourierId::new("courier-1")))
                        .is_ok_and(|result| result.to == to)
                })
                .map(|trigger| TransitionResult { from, trigger, to });
            assert_eq!(applied_transition(from, to), expected, "{from:?} -> {to:?}");
        }
    }
    assert_eq!(applied_transition(State::Waiting, State::PickedUp), None);
}

#[test]
fn terminal_states_are_the_states_without_an_allowed_trigger() {
    let terminal: Vec<State> = ALL_STATES
        .into_iter()
        .filter(|state| state.is_terminal())
        .collect();
    assert_eq!(
        terminal,
        vec![State::Delivered, State::Failed, State::Cancelled]
    );
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

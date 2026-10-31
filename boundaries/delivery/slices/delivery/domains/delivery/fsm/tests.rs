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

#[derive(Clone, Copy, Debug)]
enum Expected {
    To(State),
    Invalid,
    Repeat,
}

/// Expected outcome of every leaf `state × trigger` pair, written
/// independently of the table and the hierarchy.
const EXPECTED: [(State, [Expected; 6]); 6] = {
    use Expected::{Invalid as I, Repeat as R, To};
    // Assign, Unassign, Pickup, Complete, Fail, Cancel
    [
        (
            State::Waiting,
            [To(State::Assigned), I, I, I, I, To(State::Cancelled)],
        ),
        (
            State::Assigned,
            [
                R,
                To(State::Waiting),
                To(State::PickedUp),
                I,
                To(State::Failed),
                To(State::Cancelled),
            ],
        ),
        (
            State::PickedUp,
            [I, I, R, To(State::Delivered), To(State::Failed), I],
        ),
        (State::Delivered, [I, I, I, R, I, I]),
        (State::Failed, [I, I, I, I, R, I]),
        (State::Cancelled, [I, I, I, I, I, R]),
    ]
};

#[test]
fn every_state_trigger_pair_has_the_expected_explicit_outcome() {
    let courier = CourierId::new("courier-1");
    assert_eq!(
        EXPECTED.map(|(state, _)| state),
        ALL_STATES,
        "matrix covers every leaf state"
    );
    for (from, outcomes) in EXPECTED {
        for (trigger, expected) in ALL_TRIGGERS.into_iter().zip(outcomes) {
            let want = match expected {
                Expected::To(to) => Ok(TransitionResult { from, trigger, to }),
                Expected::Invalid => Err(TransitionError::InvalidTransition { from, trigger }),
                Expected::Repeat => Err(TransitionError::AlreadyInState { state: from }),
            };
            assert_eq!(
                transition(from, trigger, Some(&courier)),
                want,
                "{from:?} x {trigger:?}"
            );
        }
    }
}

#[test]
fn compound_states_group_the_expected_leaf_states() {
    assert_eq!(
        State::leaves_of(CompoundState::ActiveAssignment).collect::<Vec<_>>(),
        [State::Assigned, State::PickedUp]
    );
    assert_eq!(
        State::leaves_of(CompoundState::Closed).collect::<Vec<_>>(),
        [State::Delivered, State::Failed, State::Cancelled]
    );
    assert!(State::PickedUp.is_in(CompoundState::ActiveAssignment));
    assert!(!State::Waiting.is_in(CompoundState::ActiveAssignment));
    assert!(State::Waiting.is_in(State::Waiting));
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
    for state in terminal {
        assert!(
            ALL_TRIGGERS
                .into_iter()
                .all(|trigger| ensure_allowed(state, trigger).is_err()),
            "{state:?}"
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

mod machine;

use std::error::Error;
use std::fmt::{Display, Formatter};

use crate::slices::courier::domains::courier::CourierId;

pub use machine::{CompoundState, Node, State, TransitionResult, Trigger, applied_transition};
use machine::{Outcome, outcome};
pub(super) use machine::{in_future, requires_courier};

impl State {
    pub const ALL: [Self; 6] = [
        Self::Waiting,
        Self::Assigned,
        Self::PickedUp,
        Self::Delivered,
        Self::Failed,
        Self::Cancelled,
    ];

    /// Whether the state is `node` or lies inside it.
    #[must_use]
    pub fn is_in(self, node: impl Into<Node>) -> bool {
        self.in_node(node.into())
    }

    /// Leaf states inside `node`, in declaration order.
    pub fn leaves_of(node: impl Into<Node>) -> impl Iterator<Item = Self> {
        let node = node.into();
        Self::ALL.into_iter().filter(move |state| state.is_in(node))
    }
}

impl CompoundState {
    pub const ALL: [Self; 2] = [Self::ActiveAssignment, Self::Closed];
}

impl From<State> for Node {
    fn from(state: State) -> Self {
        Self::Leaf(state)
    }
}

impl From<CompoundState> for Node {
    fn from(compound: CompoundState) -> Self {
        Self::Compound(compound)
    }
}

impl Trigger {
    pub const ALL: [Self; 6] = [
        Self::Assign,
        Self::Unassign,
        Self::Pickup,
        Self::Complete,
        Self::Fail,
        Self::Cancel,
    ];
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

/// Checks the table outcome for `(from, trigger)` without evaluating guards.
pub(super) fn ensure_allowed(from: State, trigger: Trigger) -> Result<(), TransitionError> {
    match outcome(from, trigger) {
        Outcome::Allowed { .. } => Ok(()),
        Outcome::Invalid => Err(TransitionError::InvalidTransition { from, trigger }),
        Outcome::AlreadyApplied => Err(TransitionError::AlreadyInState { state: from }),
    }
}

pub(super) fn transition(
    from: State,
    trigger: Trigger,
    courier_id: Option<&CourierId>,
) -> Result<TransitionResult, TransitionError> {
    match outcome(from, trigger) {
        Outcome::Allowed {
            courier_required: true,
            ..
        } if courier_id.is_none() => Err(TransitionError::GuardFailed {
            reason: "courier_id_required",
        }),
        Outcome::Allowed { to, .. } => Ok(TransitionResult { from, trigger, to }),
        Outcome::Invalid => Err(TransitionError::InvalidTransition { from, trigger }),
        Outcome::AlreadyApplied => Err(TransitionError::AlreadyInState { state: from }),
    }
}

#[cfg(test)]
mod tests;

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
        lineage(Node::Leaf(self)).contains(&node.into())
    }

    /// Leaf states inside `node`, in declaration order.
    pub fn leaves_of(node: impl Into<Node>) -> impl Iterator<Item = Self> {
        let node = node.into();
        Self::ALL.into_iter().filter(move |state| state.is_in(node))
    }

    /// Terminal states form the `Closed` compound state.
    #[must_use]
    pub fn is_terminal(self) -> bool {
        self.is_in(CompoundState::Closed)
    }
}

/// Compound states: named groups of the Ubiquitous Language. They are never
/// persisted or published; the delivery is always in exactly one leaf state.
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
pub enum CompoundState {
    /// Active Courier Assignment: the courier acts on the delivery and sees
    /// its order (FR-444). Storage adapters filter by its leaves;
    /// [`Delivery::active_courier`](super::Delivery::active_courier) is the
    /// rule for a loaded aggregate.
    ActiveAssignment,
    /// Terminal states.
    Closed,
}

impl CompoundState {
    pub const ALL: [Self; 2] = [Self::ActiveAssignment, Self::Closed];
}

#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
pub enum Node {
    Leaf(State),
    Compound(CompoundState),
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

/// Parent of every node; `None` marks a top-level node.
const fn parent(node: Node) -> Option<CompoundState> {
    match node {
        Node::Leaf(State::Waiting)
        | Node::Compound(CompoundState::ActiveAssignment | CompoundState::Closed) => None,
        Node::Leaf(State::Assigned | State::PickedUp) => Some(CompoundState::ActiveAssignment),
        Node::Leaf(State::Delivered | State::Failed | State::Cancelled) => {
            Some(CompoundState::Closed)
        }
    }
}

/// `node` followed by its ancestors up to the top level.
fn lineage(node: Node) -> Vec<Node> {
    let mut nodes = vec![node];
    let mut current = node;
    while let Some(compound) = parent(current) {
        current = Node::Compound(compound);
        nodes.push(current);
    }
    nodes
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
enum Guard {
    CourierRequired,
}

#[derive(Clone, Copy)]
struct TransitionRow {
    source: Node,
    trigger: Trigger,
    target: State,
    guard: Option<Guard>,
}

/// Transitions by source node: a row on a compound state applies to every
/// state inside it, and the innermost row wins. `Cancel` is declared on
/// `Waiting` and `Assigned`: Delivery Cancellation before Pickup crosses the
/// Active Courier Assignment boundary, and a node has one parent.
const TRANSITIONS: [TransitionRow; 7] = [
    row(
        Node::Leaf(State::Waiting),
        Trigger::Assign,
        State::Assigned,
        Some(Guard::CourierRequired),
    ),
    row(
        Node::Leaf(State::Waiting),
        Trigger::Cancel,
        State::Cancelled,
        None,
    ),
    row(
        Node::Leaf(State::Assigned),
        Trigger::Unassign,
        State::Waiting,
        None,
    ),
    row(
        Node::Leaf(State::Assigned),
        Trigger::Pickup,
        State::PickedUp,
        None,
    ),
    row(
        Node::Leaf(State::Assigned),
        Trigger::Cancel,
        State::Cancelled,
        None,
    ),
    row(
        Node::Leaf(State::PickedUp),
        Trigger::Complete,
        State::Delivered,
        None,
    ),
    row(
        Node::Compound(CompoundState::ActiveAssignment),
        Trigger::Fail,
        State::Failed,
        None,
    ),
];

/// Strict repeats of an already applied transition: explicit
/// `AlreadyInState`. Declared on leaf states only.
const REPEATS: [(State, Trigger); 5] = [
    (State::Assigned, Trigger::Assign),
    (State::PickedUp, Trigger::Pickup),
    (State::Delivered, Trigger::Complete),
    (State::Failed, Trigger::Fail),
    (State::Cancelled, Trigger::Cancel),
];

const fn row(source: Node, trigger: Trigger, target: State, guard: Option<Guard>) -> TransitionRow {
    TransitionRow {
        source,
        trigger,
        target,
        guard,
    }
}

/// Finds the outcome for `(from, trigger)` without evaluating guards: the
/// innermost declaration on `from` or one of its ancestors wins.
fn resolve(
    from: State,
    trigger: Trigger,
) -> Result<(TransitionResult, Option<Guard>), TransitionError> {
    for node in lineage(Node::Leaf(from)) {
        if let Some(rule) = TRANSITIONS
            .iter()
            .find(|rule| rule.source == node && rule.trigger == trigger)
        {
            let to = rule.target;
            return Ok((TransitionResult { from, trigger, to }, rule.guard));
        }
        if node == Node::Leaf(from) && REPEATS.contains(&(from, trigger)) {
            return Err(TransitionError::AlreadyInState { state: from });
        }
    }
    Err(TransitionError::InvalidTransition { from, trigger })
}

/// Checks the table outcome for `(from, trigger)` without evaluating guards.
pub(super) fn ensure_allowed(from: State, trigger: Trigger) -> Result<(), TransitionError> {
    resolve(from, trigger).map(|_| ())
}

/// The allowed transition from `from` to `to`, if exactly one trigger
/// leads there: how a stored state change reads as a transition.
#[must_use]
pub fn applied_transition(from: State, to: State) -> Option<TransitionResult> {
    let mut allowed = Trigger::ALL
        .into_iter()
        .filter_map(|trigger| resolve(from, trigger).ok())
        .map(|(result, _)| result)
        .filter(|result| result.to == to);
    let result = allowed.next()?;
    if allowed.next().is_some() {
        return None;
    }
    Some(result)
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
mod tests;

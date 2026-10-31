//! Pure core of the `Delivery` state machine, without dependencies and
//! iterators: `verification/` extracts this file to Lean and proves its
//! invariants (`docs/testing.md`).

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
    /// Whether the state is `node` or lies inside it.
    #[must_use]
    pub fn in_node(self, node: Node) -> bool {
        match node {
            Node::Leaf(state) => self == state,
            Node::Compound(compound) => match parent(Node::Leaf(self)) {
                Some(group) => group == compound,
                None => false,
            },
        }
    }

    /// Terminal states form the `Closed` compound state.
    #[must_use]
    pub fn is_terminal(self) -> bool {
        self.in_node(Node::Compound(CompoundState::Closed))
    }
}

/// Compound states: named groups of the Ubiquitous Language. They are never
/// persisted or published; the delivery is always in exactly one leaf state.
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
pub enum CompoundState {
    /// Active Courier Assignment: the courier acts on the delivery and sees
    /// its order (FR-444). Storage adapters filter by its leaves;
    /// `Delivery::active_courier` is the rule for a loaded aggregate.
    ActiveAssignment,
    /// Terminal states.
    Closed,
}

#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
pub enum Node {
    Leaf(State),
    Compound(CompoundState),
}

/// Parent of every node; `None` marks a top-level node.
#[must_use]
pub fn parent(node: Node) -> Option<CompoundState> {
    match node {
        Node::Leaf(State::Waiting) | Node::Compound(_) => None,
        Node::Leaf(State::Assigned | State::PickedUp) => Some(CompoundState::ActiveAssignment),
        Node::Leaf(State::Delivered | State::Failed | State::Cancelled) => {
            Some(CompoundState::Closed)
        }
    }
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

#[derive(Clone, Copy)]
pub enum Outcome {
    Allowed { to: State, courier_required: bool },
    Invalid,
    AlreadyApplied,
}

/// The transition table: every state and trigger has exactly one outcome.
/// `Cancel` is allowed in `Waiting` and `Assigned`: Delivery Cancellation
/// before Pickup crosses the Active Courier Assignment boundary.
#[must_use]
pub fn outcome(from: State, trigger: Trigger) -> Outcome {
    use Outcome::{Allowed, AlreadyApplied, Invalid};
    use State::{Assigned, Cancelled, Delivered, Failed, PickedUp, Waiting};
    use Trigger::{Assign, Cancel, Complete, Fail, Pickup, Unassign};

    match (from, trigger) {
        (Waiting, Assign) => Allowed {
            to: Assigned,
            courier_required: true,
        },
        (Waiting | Assigned, Cancel) => Allowed {
            to: Cancelled,
            courier_required: false,
        },
        (Assigned, Unassign) => Allowed {
            to: Waiting,
            courier_required: false,
        },
        (Assigned, Pickup) => Allowed {
            to: PickedUp,
            courier_required: false,
        },
        (PickedUp, Complete) => Allowed {
            to: Delivered,
            courier_required: false,
        },
        (Assigned | PickedUp, Fail) => Allowed {
            to: Failed,
            courier_required: false,
        },
        (Assigned, Assign)
        | (PickedUp, Pickup)
        | (Delivered, Complete)
        | (Failed, Fail)
        | (Cancelled, Cancel) => AlreadyApplied,
        (Waiting, Unassign | Pickup | Complete | Fail)
        | (Assigned, Complete)
        | (PickedUp, Assign | Unassign | Cancel)
        | (Delivered, Assign | Unassign | Pickup | Fail | Cancel)
        | (Failed, Assign | Unassign | Pickup | Complete | Cancel)
        | (Cancelled, Assign | Unassign | Pickup | Complete | Fail) => Invalid,
    }
}

/// Whether a stored Delivery in `state` must have a courier; otherwise it must
/// have none. A delivered or failed delivery keeps the courier who carried it.
#[must_use]
pub fn requires_courier(state: State) -> bool {
    match state {
        State::Assigned | State::PickedUp | State::Delivered | State::Failed => true,
        State::Waiting | State::Cancelled => false,
    }
}

/// Whether an expected time named at `now_epoch_seconds` is in the future.
#[must_use]
pub fn in_future(epoch_seconds: i64, now_epoch_seconds: i64) -> bool {
    epoch_seconds > now_epoch_seconds
}

fn leads_to(from: State, trigger: Trigger, to: State) -> bool {
    match outcome(from, trigger) {
        Outcome::Allowed { to: target, .. } => target == to,
        Outcome::Invalid | Outcome::AlreadyApplied => false,
    }
}

/// The allowed transition from `from` to `to`, if exactly one trigger
/// leads there: how a stored state change reads as a transition.
#[must_use]
pub fn applied_transition(from: State, to: State) -> Option<TransitionResult> {
    let trigger = match (
        leads_to(from, Trigger::Assign, to),
        leads_to(from, Trigger::Unassign, to),
        leads_to(from, Trigger::Pickup, to),
        leads_to(from, Trigger::Complete, to),
        leads_to(from, Trigger::Fail, to),
        leads_to(from, Trigger::Cancel, to),
    ) {
        (true, false, false, false, false, false) => Trigger::Assign,
        (false, true, false, false, false, false) => Trigger::Unassign,
        (false, false, true, false, false, false) => Trigger::Pickup,
        (false, false, false, true, false, false) => Trigger::Complete,
        (false, false, false, false, true, false) => Trigger::Fail,
        (false, false, false, false, false, true) => Trigger::Cancel,
        _ => return None,
    };
    Some(TransitionResult { from, trigger, to })
}

/// Numeric parameters of the assignment process. Values are product
/// decisions supplied by the composition root, not domain constants.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct AssignmentPolicy {
    /// Inclusive upper bound of the distance from courier to pickup.
    pub maximum_distance_meters: u32,
    /// Capacity is exhausted when active assignments reach this value.
    pub maximum_active_assignments: u32,
    /// Lifetime of a Delivery Offer; must be positive.
    pub offer_ttl_seconds: i64,
}

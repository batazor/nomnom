use crate::pkg::application::{AuthorizationError, Subject};

/// Authorization port of the `simulator` slice: `platform#operate_simulator`
/// (FR-604). The SpiceDB adapter lives in `infrastructure/`.
pub trait SimulatorAuthorization {
    fn check_operate_simulator(&self, subject: &Subject) -> Result<(), AuthorizationError>;
}

//! Get Simulator: оператор платформы читает переключатель Simulator
//! (`platform#operate_simulator`, FR-604).

use crate::pkg::application::Subject;
use crate::slices::simulator::domains::simulator::SimulatorStateRepository;

use super::authorization::SimulatorAuthorization;
use super::error::SimulatorApplicationError;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct Output {
    pub enabled: bool,
}

pub struct Handler<'a> {
    pub authorization: &'a dyn SimulatorAuthorization,
    pub state: &'a dyn SimulatorStateRepository,
}

impl Handler<'_> {
    pub fn execute(&self, subject: &Subject) -> Result<Output, SimulatorApplicationError> {
        self.authorization.check_operate_simulator(subject)?;
        Ok(Output {
            enabled: self.state.enabled()?,
        })
    }
}

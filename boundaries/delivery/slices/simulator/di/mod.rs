//! Shaku-модуль слайса `simulator`. Реализации портов выбирает корневой `di`.

use std::sync::Arc;

use shaku::{Component, HasComponent, Interface, module};

use crate::pkg::application::Subject;
use crate::slices::simulator::applications::authorization::SimulatorAuthorization;
use crate::slices::simulator::applications::error::SimulatorApplicationError;
use crate::slices::simulator::applications::work::SimulatorWork;
use crate::slices::simulator::applications::{get_simulator, set_simulator_enabled};
use crate::slices::simulator::domains::simulator::SimulatorStateRepository;

/// Роль хранилища слайса: переключатель и выборка работы Simulator.
pub trait SimulatorStore: SimulatorStateRepository + SimulatorWork + Send + Sync {}

impl<T: SimulatorStateRepository + SimulatorWork + Send + Sync> SimulatorStore for T {}

/// Внешние порты слайса.
pub trait SimulatorPorts: Interface {
    fn authorization(&self) -> &dyn SimulatorAuthorization;
    fn store(&self) -> &dyn SimulatorStore;
}

/// Модуль, через который корневой `di` передаёт слайсу порты.
pub trait SimulatorDependencies: HasComponent<dyn SimulatorPorts> {}

pub trait SimulatorUseCases: Interface {
    fn get_simulator(
        &self,
        subject: &Subject,
    ) -> Result<get_simulator::Output, SimulatorApplicationError>;

    fn set_simulator_enabled(
        &self,
        subject: &Subject,
        input: set_simulator_enabled::Input,
    ) -> Result<set_simulator_enabled::Output, SimulatorApplicationError>;
}

#[derive(Component)]
#[shaku(interface = SimulatorUseCases)]
pub struct SimulatorUseCasesImpl {
    #[shaku(inject)]
    ports: Arc<dyn SimulatorPorts>,
}

impl SimulatorUseCases for SimulatorUseCasesImpl {
    fn get_simulator(
        &self,
        subject: &Subject,
    ) -> Result<get_simulator::Output, SimulatorApplicationError> {
        get_simulator::Handler {
            authorization: self.ports.authorization(),
            state: self.ports.store(),
        }
        .execute(subject)
    }

    fn set_simulator_enabled(
        &self,
        subject: &Subject,
        input: set_simulator_enabled::Input,
    ) -> Result<set_simulator_enabled::Output, SimulatorApplicationError> {
        set_simulator_enabled::Handler {
            authorization: self.ports.authorization(),
            state: self.ports.store(),
        }
        .execute(subject, input)
    }
}

module! {
    pub SimulatorModule {
        components = [SimulatorUseCasesImpl],
        providers = [],

        use dyn SimulatorDependencies {
            components = [dyn SimulatorPorts],
            providers = []
        }
    }
}

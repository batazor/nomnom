//! Set Simulator Enabled: оператор платформы включает или выключает
//! Simulator (`platform#operate_simulator`, FR-604). Состояние сохраняется в
//! хранилище сервиса и переживает рестарт.

use crate::pkg::authz::Subject;
use crate::slices::simulator::domains::simulator::SimulatorStateRepository;

use super::authorization::SimulatorAuthorization;
use super::error::SimulatorApplicationError;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct Input {
    pub enabled: bool,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct Output {
    pub enabled: bool,
}

pub struct Handler<'a> {
    pub authorization: &'a dyn SimulatorAuthorization,
    pub state: &'a dyn SimulatorStateRepository,
}

impl Handler<'_> {
    pub fn execute(
        &self,
        subject: &Subject,
        input: Input,
    ) -> Result<Output, SimulatorApplicationError> {
        self.authorization.check_operate_simulator(subject)?;
        self.state.set_enabled(input.enabled)?;
        Ok(Output {
            enabled: input.enabled,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::memory::StaticAuthorization;
    use crate::infrastructure::repository::test_support::TestStore;
    use crate::slices::simulator::applications::get_simulator;

    fn operator() -> Subject {
        Subject::User("operator-1".to_owned())
    }

    fn operator_authorization() -> StaticAuthorization {
        let authorization = StaticAuthorization::grants_only();
        authorization.grant_simulator(operator());
        authorization
    }

    fn get(
        authorization: &StaticAuthorization,
        store: &TestStore,
        subject: &Subject,
    ) -> Result<get_simulator::Output, SimulatorApplicationError> {
        get_simulator::Handler {
            authorization,
            state: store,
        }
        .execute(subject)
    }

    fn set(
        authorization: &StaticAuthorization,
        store: &TestStore,
        subject: &Subject,
        enabled: bool,
    ) -> Result<Output, SimulatorApplicationError> {
        Handler {
            authorization,
            state: store,
        }
        .execute(subject, Input { enabled })
    }

    /// FR-604: a new service starts with the Simulator disabled.
    #[test]
    fn simulator_is_disabled_until_enabled() {
        let store = TestStore::new();

        let output = get(&operator_authorization(), &store, &operator()).expect("read");

        assert!(!output.enabled);
    }

    /// AC-761: the state is stored and survives a restart.
    #[test]
    fn operator_enables_and_disables_the_simulator() {
        let store = TestStore::new();
        let authorization = operator_authorization();

        let enabled = set(&authorization, &store, &operator(), true).expect("enable");
        assert!(enabled.enabled);
        // A fresh handler over the same storage is a restarted process.
        assert!(
            get(&operator_authorization(), &store, &operator())
                .expect("read")
                .enabled
        );

        set(&authorization, &store, &operator(), false).expect("disable");
        assert!(
            !get(&authorization, &store, &operator())
                .expect("read")
                .enabled
        );
    }

    /// AC-762
    #[test]
    fn subject_without_operate_simulator_is_denied_and_nothing_changes() {
        let store = TestStore::new();
        let authorization = operator_authorization();
        let customer = Subject::User("customer-1".to_owned());

        assert_eq!(
            set(&authorization, &store, &customer, true),
            Err(SimulatorApplicationError::PermissionDenied)
        );
        assert_eq!(
            get(&authorization, &store, &customer),
            Err(SimulatorApplicationError::PermissionDenied)
        );
        assert!(
            !get(&authorization, &store, &operator())
                .expect("read")
                .enabled
        );
    }

    #[test]
    fn unavailable_authorization_fails_closed() {
        let store = TestStore::new();

        assert!(matches!(
            set(
                &StaticAuthorization::unavailable(),
                &store,
                &operator(),
                true
            ),
            Err(SimulatorApplicationError::DependencyUnavailable(_))
        ));
        assert!(
            !get(&operator_authorization(), &store, &operator())
                .expect("read")
                .enabled
        );
    }
}

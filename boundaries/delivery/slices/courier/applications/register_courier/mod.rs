//! Register Courier: a platform operator creates a `Courier` that is
//! available for new assignments. Before the courier is persisted,
//! `courier:<id>#self@user:<id>` is written to the authorization graph; a
//! failed write aborts the registration (fail closed).

use crate::pkg::application::Subject;
use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::domains::courier::{
    Courier, CourierId, CourierRepository, CourierStatus,
};

use super::authorization::{CourierAuthorization, CourierPermission, CourierResource};
use super::courier_id;
use super::error::CourierApplicationError;
use super::relationships::CourierRelationshipWriter;

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    pub courier_id: String,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Output {
    pub courier_id: CourierId,
    pub status: CourierStatus,
}

pub struct Handler<'a> {
    pub authorization: &'a dyn CourierAuthorization,
    pub couriers: &'a dyn CourierRepository,
    pub relationships: &'a dyn CourierRelationshipWriter,
}

impl Handler<'_> {
    pub fn execute(
        &self,
        subject: &Subject,
        input: Input,
    ) -> Result<Output, CourierApplicationError> {
        let courier_id = courier_id(&input.courier_id)?;
        self.authorization.check(
            subject,
            CourierPermission::RegisterCourier,
            &CourierResource::Platform,
        )?;

        if self.couriers.get(&courier_id)?.is_some() {
            return Err(CourierApplicationError::CourierAlreadyRegistered(
                courier_id,
            ));
        }
        // A repeated registration was rejected above and writes nothing.
        let revision = self.relationships.register_courier(&courier_id)?;
        let mut courier = Courier::new(courier_id);
        // FR-447: checks of the courier are at least as fresh as this write.
        courier.record_authorization_revision(revision);
        match self.couriers.add(&courier) {
            Ok(()) => Ok(Output {
                courier_id: courier.id().clone(),
                status: courier.status(),
            }),
            Err(RepositoryError::Conflict) => Err(
                CourierApplicationError::CourierAlreadyRegistered(courier.id().clone()),
            ),
            Err(error) => Err(error.into()),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::memory::{
        MemoryStore, RecordingRelationships, RelationshipWrite, StaticAuthorization,
    };
    use crate::pkg::application::RelationshipWriteError;
    use crate::pkg::persistence::AuthorizationRevision;
    use crate::slices::courier::applications::error::InvalidCourierRequest;

    fn operator() -> Subject {
        Subject::User("operator-1".to_owned())
    }

    fn input(courier_id: &str) -> Input {
        Input {
            courier_id: courier_id.to_owned(),
        }
    }

    /// AC-302
    #[test]
    fn registers_available_courier() {
        let store = MemoryStore::new();
        let authorization = StaticAuthorization::allow_all();
        let relationships = RecordingRelationships::new();
        let handler = Handler {
            authorization: &authorization,
            couriers: &store,
            relationships: &relationships,
        };

        let output = handler
            .execute(&operator(), input(" courier-1 "))
            .expect("courier registered");

        assert_eq!(output.courier_id, CourierId::new("courier-1"));
        assert_eq!(output.status, CourierStatus::Available);
        let mut expected = Courier::new(CourierId::new("courier-1"));
        expected.record_authorization_revision(RecordingRelationships::revision(1));
        assert_eq!(store.get(&CourierId::new("courier-1")), Ok(Some(expected)));
        assert_eq!(
            authorization.courier_checks(),
            vec![(
                operator(),
                CourierPermission::RegisterCourier,
                CourierResource::Platform
            )]
        );
        assert_eq!(
            relationships.writes(),
            vec![RelationshipWrite::RegisterCourier(CourierId::new(
                "courier-1"
            ))]
        );
    }

    /// Writer that observes whether the courier was already persisted.
    struct ObservingWriter<'a> {
        store: &'a MemoryStore,
        persisted_at_write: std::cell::Cell<Option<bool>>,
    }

    impl CourierRelationshipWriter for ObservingWriter<'_> {
        fn register_courier(
            &self,
            courier_id: &CourierId,
        ) -> Result<AuthorizationRevision, RelationshipWriteError> {
            let persisted = self.store.get(courier_id).expect("read courier").is_some();
            self.persisted_at_write.set(Some(persisted));
            Ok(AuthorizationRevision::new("observed"))
        }
    }

    /// FR-444: the relationship is written before the courier is persisted.
    #[test]
    fn relationship_is_written_before_courier_is_persisted() {
        let store = MemoryStore::new();
        let authorization = StaticAuthorization::allow_all();
        let writer = ObservingWriter {
            store: &store,
            persisted_at_write: std::cell::Cell::new(None),
        };
        let handler = Handler {
            authorization: &authorization,
            couriers: &store,
            relationships: &writer,
        };

        handler
            .execute(&operator(), input("courier-1"))
            .expect("courier registered");

        assert_eq!(writer.persisted_at_write.get(), Some(false));
        assert!(
            store
                .get(&CourierId::new("courier-1"))
                .expect("read")
                .is_some()
        );
    }

    /// FR-444: a failed write aborts the registration (fail closed).
    #[test]
    fn failed_relationship_write_persists_nothing() {
        let store = MemoryStore::new();
        let authorization = StaticAuthorization::allow_all();
        let relationships = RecordingRelationships::failing("spicedb offline");
        let handler = Handler {
            authorization: &authorization,
            couriers: &store,
            relationships: &relationships,
        };

        assert!(matches!(
            handler.execute(&operator(), input("courier-1")),
            Err(CourierApplicationError::DependencyUnavailable(reason)) if reason.contains("spicedb offline")
        ));
        assert_eq!(store.get(&CourierId::new("courier-1")), Ok(None));
    }

    /// AC-303
    #[test]
    fn repeated_registration_is_rejected_without_changes() {
        let store = MemoryStore::new();
        let existing = Courier::restore(CourierId::new("courier-1"), CourierStatus::Busy);
        store.add(&existing).expect("seed courier");
        let authorization = StaticAuthorization::allow_all();
        let relationships = RecordingRelationships::new();
        let handler = Handler {
            authorization: &authorization,
            couriers: &store,
            relationships: &relationships,
        };

        assert_eq!(
            handler.execute(&operator(), input("courier-1")),
            Err(CourierApplicationError::CourierAlreadyRegistered(
                CourierId::new("courier-1")
            ))
        );
        assert_eq!(store.get(&CourierId::new("courier-1")), Ok(Some(existing)));
        assert!(relationships.writes().is_empty());
    }

    /// AC-301
    #[test]
    fn denied_or_unavailable_authorization_fails_closed() {
        let store = MemoryStore::new();
        let relationships = RecordingRelationships::new();
        for (authorization, expected) in [
            (
                StaticAuthorization::deny_all(),
                CourierApplicationError::PermissionDenied,
            ),
            (
                StaticAuthorization::unavailable(),
                CourierApplicationError::DependencyUnavailable("authorization offline".to_owned()),
            ),
        ] {
            let handler = Handler {
                authorization: &authorization,
                couriers: &store,
                relationships: &relationships,
            };
            assert_eq!(
                handler.execute(&operator(), input("courier-1")),
                Err(expected)
            );
        }
        assert_eq!(store.get(&CourierId::new("courier-1")), Ok(None));
        assert!(relationships.writes().is_empty());
    }

    #[test]
    fn blank_courier_id_is_invalid() {
        let store = MemoryStore::new();
        let authorization = StaticAuthorization::allow_all();
        let relationships = RecordingRelationships::new();
        let handler = Handler {
            authorization: &authorization,
            couriers: &store,
            relationships: &relationships,
        };
        assert_eq!(
            handler.execute(&operator(), input("  ")),
            Err(CourierApplicationError::InvalidRequest(
                InvalidCourierRequest::EmptyCourierId
            ))
        );
    }

    #[test]
    fn storage_outage_is_dependency_unavailable() {
        let store = MemoryStore::new();
        store.set_unavailable(true);
        let authorization = StaticAuthorization::allow_all();
        let relationships = RecordingRelationships::new();
        let handler = Handler {
            authorization: &authorization,
            couriers: &store,
            relationships: &relationships,
        };
        assert!(matches!(
            handler.execute(&operator(), input("courier-1")),
            Err(CourierApplicationError::DependencyUnavailable(_))
        ));
    }
}

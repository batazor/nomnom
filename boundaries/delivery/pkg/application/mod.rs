//! Shared application-level ports of the `delivery` bounded context.
//!
//! Slice-specific ports (authorization permissions, Outbox, read models)
//! live next to the consuming use cases in `slices/<slice>/applications/`.

use std::error::Error;
use std::fmt::{Display, Formatter};

/// Verified identity that invokes a use case.
#[derive(Clone, Debug, Eq, Hash, PartialEq)]
pub enum Subject {
    /// End user: courier, dispatcher or platform operator.
    User(String),
    /// Workload identity: Temporal Activity worker, consumer, background job.
    Service(String),
}

/// Outcome of an authoritative permission check that did not grant access.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum AuthorizationError {
    /// The subject is not allowed to perform the permission on the resource.
    Denied,
    /// The decision could not be made; protected operations fail closed.
    Unavailable(String),
}

impl Display for AuthorizationError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Denied => formatter.write_str("permission denied"),
            Self::Unavailable(reason) => write!(formatter, "authorization unavailable: {reason}"),
        }
    }
}

impl Error for AuthorizationError {}

/// A relationship write that did not reach the authorization graph. Writers
/// run before the local commit, so the use case fails closed and persists
/// nothing (dual-write rules in `docs/security.md`).
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RelationshipWriteError(pub String);

impl Display for RelationshipWriteError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        write!(formatter, "relationship write failed: {}", self.0)
    }
}

impl Error for RelationshipWriteError {}

/// Source of the current time for application code; domain code receives
/// the value explicitly.
pub trait Clock {
    fn now_epoch_seconds(&self) -> i64;
}

/// Generates identifiers of new aggregates before a transaction starts.
pub trait IdGenerator {
    fn next_id(&self) -> String;
}

/// Failure of the local transaction itself, independent of the work inside it.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum UnitOfWorkError {
    /// The transaction could not be committed; nothing was persisted.
    CommitFailed(String),
    /// The commit outcome is unknown; callers resolve it by idempotent retry.
    CommitOutcomeUnknown,
}

impl Display for UnitOfWorkError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::CommitFailed(reason) => write!(formatter, "transaction commit failed: {reason}"),
            Self::CommitOutcomeUnknown => formatter.write_str("transaction commit outcome unknown"),
        }
    }
}

impl Error for UnitOfWorkError {}

/// Local transaction scope of the `delivery` storage.
///
/// `work` runs inside one transaction: a successful result is committed,
/// an error rolls back every repository and Outbox write made inside it.
/// Only local storage operations are allowed inside `work`.
pub trait UnitOfWork {
    fn run<T, E, F>(&self, work: F) -> Result<T, E>
    where
        F: FnOnce() -> Result<T, E>,
        E: From<UnitOfWorkError>;
}

/// Object-safe форма [`UnitOfWork`]: generic `run` нельзя вызвать через `dyn`.
pub trait TransactionScope {
    /// Выполняет `work` в одной транзакции; `work` возвращает `true`, если
    /// транзакцию нужно зафиксировать, и `false` для отката.
    fn run_scoped(&self, work: &mut dyn FnMut() -> bool) -> Result<(), UnitOfWorkError>;
}

enum ScopeOutcome {
    RolledBack,
    Failed(UnitOfWorkError),
}

impl From<UnitOfWorkError> for ScopeOutcome {
    fn from(error: UnitOfWorkError) -> Self {
        Self::Failed(error)
    }
}

impl<U: UnitOfWork> TransactionScope for U {
    fn run_scoped(&self, work: &mut dyn FnMut() -> bool) -> Result<(), UnitOfWorkError> {
        let outcome = self.run(|| {
            if work() {
                Ok(())
            } else {
                Err(ScopeOutcome::RolledBack)
            }
        });
        match outcome {
            Ok(()) | Err(ScopeOutcome::RolledBack) => Ok(()),
            Err(ScopeOutcome::Failed(error)) => Err(error),
        }
    }
}

/// [`UnitOfWork`] поверх [`TransactionScope`]: позволяет передать generic
/// handlers транзакцию, полученную через `dyn`.
pub struct ScopedUnitOfWork<'a>(&'a dyn TransactionScope);

impl<'a> ScopedUnitOfWork<'a> {
    #[must_use]
    pub fn new(scope: &'a dyn TransactionScope) -> Self {
        Self(scope)
    }
}

impl UnitOfWork for ScopedUnitOfWork<'_> {
    fn run<T, E, F>(&self, work: F) -> Result<T, E>
    where
        F: FnOnce() -> Result<T, E>,
        E: From<UnitOfWorkError>,
    {
        let mut work = Some(work);
        let mut result = None;
        let outcome = self.0.run_scoped(&mut || {
            let value = work.take().map(|work| work());
            let commit = matches!(value, Some(Ok(_)));
            result = value;
            commit
        });
        match (outcome, result) {
            (Ok(()), Some(result)) => result,
            (Err(error), _) => Err(E::from(error)),
            (Ok(()), None) => Err(E::from(UnitOfWorkError::CommitFailed(
                "unit of work did not run the work".to_owned(),
            ))),
        }
    }
}

/// Input validation of a required identifier: surrounding whitespace is
/// removed and an empty value is rejected.
#[must_use]
pub fn required_identifier(value: &str) -> Option<&str> {
    let trimmed = value.trim();
    (!trimmed.is_empty()).then_some(trimmed)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn required_identifier_trims_and_rejects_blank_values() {
        assert_eq!(required_identifier(" courier-1 "), Some("courier-1"));
        assert_eq!(required_identifier(" \t "), None);
    }

    #[derive(Default)]
    struct RecordingUnitOfWork {
        fail_commit: Option<UnitOfWorkError>,
        commits: std::cell::Cell<usize>,
        rollbacks: std::cell::Cell<usize>,
    }

    impl UnitOfWork for RecordingUnitOfWork {
        fn run<T, E, F>(&self, work: F) -> Result<T, E>
        where
            F: FnOnce() -> Result<T, E>,
            E: From<UnitOfWorkError>,
        {
            let result = work().and_then(|value| {
                self.fail_commit
                    .clone()
                    .map_or(Ok(value), |error| Err(E::from(error)))
            });
            let counter = if result.is_ok() {
                &self.commits
            } else {
                &self.rollbacks
            };
            counter.set(counter.get() + 1);
            result
        }
    }

    #[derive(Debug, PartialEq)]
    enum TestError {
        Work,
        UnitOfWork(UnitOfWorkError),
    }

    impl From<UnitOfWorkError> for TestError {
        fn from(error: UnitOfWorkError) -> Self {
            Self::UnitOfWork(error)
        }
    }

    #[test]
    fn scoped_unit_of_work_commits_successful_work() {
        let inner = RecordingUnitOfWork::default();
        let result: Result<u8, TestError> = ScopedUnitOfWork::new(&inner).run(|| Ok(7));

        assert_eq!(result, Ok(7));
        assert_eq!((inner.commits.get(), inner.rollbacks.get()), (1, 0));
    }

    #[test]
    fn scoped_unit_of_work_rolls_back_failed_work() {
        let inner = RecordingUnitOfWork::default();
        let result: Result<u8, TestError> =
            ScopedUnitOfWork::new(&inner).run(|| Err(TestError::Work));

        assert_eq!(result, Err(TestError::Work));
        assert_eq!((inner.commits.get(), inner.rollbacks.get()), (0, 1));
    }

    #[test]
    fn scoped_unit_of_work_reports_commit_failure() {
        let inner = RecordingUnitOfWork {
            fail_commit: Some(UnitOfWorkError::CommitOutcomeUnknown),
            ..RecordingUnitOfWork::default()
        };
        let result: Result<u8, TestError> = ScopedUnitOfWork::new(&inner).run(|| Ok(7));

        assert_eq!(
            result,
            Err(TestError::UnitOfWork(UnitOfWorkError::CommitOutcomeUnknown))
        );
        assert_eq!(inner.rollbacks.get(), 1);
    }
}

//! Reconciliation of the relationships owned by `delivery` (FR-445), a port of
//! the `orders` reconciler (`slices/order/infrastructure/reconcile`):
//!
//! - graph → business: `ReadRelationships` pages (`fully_consistent`); a
//!   relationship whose resource is unknown to business state or whose subject
//!   differs (orphan or stale) is deleted only when it was orphan on two passes
//!   at least `grace` apart, because writers run before commit;
//! - business → graph: every missing relationship is restored by `TOUCH`
//!   immediately and its revision is stored with the courier or delivery
//!   (FR-447); ids SpiceDB cannot store are skipped and counted. Deleting an
//!   orphan changes no revision: the resource is gone or the link is stale.
//!
//! The reconciler is a background loop of the main process ([`run`]); it
//! reads business state through [`ReconciliationStore`] (PostgreSQL in the
//! process, memory in tests).

use std::collections::{HashMap, HashSet};
use std::sync::Arc;
use std::time::{Duration, Instant};

use tokio::sync::watch;

use crate::infrastructure::memory::MemoryStore;
use crate::infrastructure::spicedb::{
    COURIER, COURIER_SELF, DELIVERY, DELIVERY_COURIER, Relationship, RelationshipGraph,
    RelationshipOperation, USER,
};
use crate::pkg::persistence::{AuthorizationRevision, RepositoryError};

/// Business state read and revision writes of reconciliation. Reads see only
/// committed state; revision writes are single-statement transactions that
/// bump the aggregate version.
pub trait ReconciliationStore: Send + Sync {
    /// Registered courier ids after `after`, ordered by id.
    fn courier_page(&self, after: &str, limit: usize) -> Result<Vec<String>, RepositoryError>;

    /// Which of `ids` are registered couriers.
    fn registered_couriers(&self, ids: &[String]) -> Result<Vec<String>, RepositoryError>;

    /// `(delivery_id, courier_id)` of assigned deliveries after `after`,
    /// ordered by delivery id.
    fn assignment_page(
        &self,
        after: &str,
        limit: usize,
    ) -> Result<Vec<(String, String)>, RepositoryError>;

    /// `(delivery_id, courier_id)` for those of `ids` with an assigned courier.
    fn assigned_couriers(&self, ids: &[String]) -> Result<Vec<(String, String)>, RepositoryError>;

    /// Stores the revision on a registered courier; `false` — not registered.
    fn record_courier_revision(
        &self,
        courier_id: &str,
        revision: AuthorizationRevision,
    ) -> Result<bool, RepositoryError>;

    /// Stores the revision on a delivery still assigned to `courier_id`.
    fn record_assignment_revision(
        &self,
        delivery_id: &str,
        courier_id: &str,
        revision: AuthorizationRevision,
    ) -> Result<bool, RepositoryError>;
}

impl ReconciliationStore for MemoryStore {
    fn courier_page(&self, after: &str, limit: usize) -> Result<Vec<String>, RepositoryError> {
        MemoryStore::courier_page(self, after, limit)
    }

    fn registered_couriers(&self, ids: &[String]) -> Result<Vec<String>, RepositoryError> {
        MemoryStore::registered_couriers(self, ids)
    }

    fn assignment_page(
        &self,
        after: &str,
        limit: usize,
    ) -> Result<Vec<(String, String)>, RepositoryError> {
        MemoryStore::assignment_page(self, after, limit)
    }

    fn assigned_couriers(&self, ids: &[String]) -> Result<Vec<(String, String)>, RepositoryError> {
        MemoryStore::assigned_couriers(self, ids)
    }

    fn record_courier_revision(
        &self,
        courier_id: &str,
        revision: AuthorizationRevision,
    ) -> Result<bool, RepositoryError> {
        MemoryStore::record_courier_revision(self, courier_id, revision)
    }

    fn record_assignment_revision(
        &self,
        delivery_id: &str,
        courier_id: &str,
        revision: AuthorizationRevision,
    ) -> Result<bool, RepositoryError> {
        MemoryStore::record_assignment_revision(self, delivery_id, courier_id, revision)
    }
}

/// Business side of one relation: `resource_id → subject_id`.
pub trait RelationSource: Send + Sync {
    /// Subjects that business state assigns to those of `resource_ids` it knows.
    fn subjects_of(&self, resource_ids: &[String]) -> Result<HashMap<String, String>, String>;

    /// `(resource_id, subject_id)` after `after`, ordered by resource id.
    fn page(&self, after: &str, limit: usize) -> Result<Vec<(String, String)>, String>;

    /// Stores the revision of a restored relationship with the resource, if
    /// business state still assigns `subject_id` to it.
    fn record_revision(
        &self,
        resource_id: &str,
        subject_id: &str,
        revision: AuthorizationRevision,
    ) -> Result<(), String>;
}

/// `courier#self`: every registered courier acts for itself.
pub struct RegisteredCouriers(pub Arc<dyn ReconciliationStore>);

impl RelationSource for RegisteredCouriers {
    fn subjects_of(&self, resource_ids: &[String]) -> Result<HashMap<String, String>, String> {
        let registered = self
            .0
            .registered_couriers(resource_ids)
            .map_err(|error| error.to_string())?;
        Ok(registered.into_iter().map(|id| (id.clone(), id)).collect())
    }

    fn page(&self, after: &str, limit: usize) -> Result<Vec<(String, String)>, String> {
        let page = self
            .0
            .courier_page(after, limit)
            .map_err(|error| error.to_string())?;
        Ok(page.into_iter().map(|id| (id.clone(), id)).collect())
    }

    fn record_revision(
        &self,
        resource_id: &str,
        _subject_id: &str,
        revision: AuthorizationRevision,
    ) -> Result<(), String> {
        self.0
            .record_courier_revision(resource_id, revision)
            .map(drop)
            .map_err(|error| error.to_string())
    }
}

/// `delivery#courier`: deliveries with `courier_id` set.
pub struct AssignedDeliveries(pub Arc<dyn ReconciliationStore>);

impl RelationSource for AssignedDeliveries {
    fn subjects_of(&self, resource_ids: &[String]) -> Result<HashMap<String, String>, String> {
        Ok(self
            .0
            .assigned_couriers(resource_ids)
            .map_err(|error| error.to_string())?
            .into_iter()
            .collect())
    }

    fn page(&self, after: &str, limit: usize) -> Result<Vec<(String, String)>, String> {
        self.0
            .assignment_page(after, limit)
            .map_err(|error| error.to_string())
    }

    fn record_revision(
        &self,
        resource_id: &str,
        subject_id: &str,
        revision: AuthorizationRevision,
    ) -> Result<(), String> {
        self.0
            .record_assignment_revision(resource_id, subject_id, revision)
            .map(drop)
            .map_err(|error| error.to_string())
    }
}

/// Drift counters of one pass.
#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub struct Stats {
    pub checked: usize,
    pub orphans_pending: usize,
    pub orphans_deleted: usize,
    pub missing_repaired: usize,
    /// Business rows whose ids SpiceDB cannot store; their writers could not
    /// have written a relationship either.
    pub unrepairable: usize,
}

/// SpiceDB object id grammar `^[a-zA-Z0-9/_|\-=+]+$`.
#[must_use]
pub fn valid_object_id(id: &str) -> bool {
    !id.is_empty()
        && id
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || matches!(c, '/' | '_' | '|' | '-' | '=' | '+'))
}

/// Source of monotonic time; tests inject a manual clock.
pub type Now = Arc<dyn Fn() -> Instant + Send + Sync>;

/// Reconciler of one relation. Orphan candidates are remembered between
/// passes in memory.
pub struct Reconciler {
    graph: Arc<dyn RelationshipGraph>,
    source: Box<dyn RelationSource>,
    resource_type: &'static str,
    relation: &'static str,
    grace: Duration,
    page_size: u32,
    now: Now,
    candidates: HashMap<Relationship, Instant>,
}

impl Reconciler {
    #[must_use]
    pub fn new(
        graph: Arc<dyn RelationshipGraph>,
        source: Box<dyn RelationSource>,
        (resource_type, relation): (&'static str, &'static str),
        grace: Duration,
        page_size: u32,
        now: Now,
    ) -> Self {
        Self {
            graph,
            source,
            resource_type,
            relation,
            grace,
            page_size: page_size.max(1),
            now,
            candidates: HashMap::new(),
        }
    }

    /// `courier#self` against registered couriers.
    #[must_use]
    pub fn couriers(
        graph: Arc<dyn RelationshipGraph>,
        store: Arc<dyn ReconciliationStore>,
        grace: Duration,
        page_size: u32,
        now: Now,
    ) -> Self {
        Self::new(
            graph,
            Box::new(RegisteredCouriers(store)),
            (COURIER, COURIER_SELF),
            grace,
            page_size,
            now,
        )
    }

    /// `delivery#courier` against `Delivery.courier_id`.
    #[must_use]
    pub fn deliveries(
        graph: Arc<dyn RelationshipGraph>,
        store: Arc<dyn ReconciliationStore>,
        grace: Duration,
        page_size: u32,
        now: Now,
    ) -> Self {
        Self::new(
            graph,
            Box::new(AssignedDeliveries(store)),
            (DELIVERY, DELIVERY_COURIER),
            grace,
            page_size,
            now,
        )
    }

    /// `resource#relation` for logs.
    #[must_use]
    pub fn name(&self) -> String {
        format!("{}#{}", self.resource_type, self.relation)
    }

    /// One pass: graph → business (orphans), then business → graph (missing).
    /// `stopping` is checked between pages; a stopped pass returns an error.
    pub fn reconcile(&mut self, stopping: &dyn Fn() -> bool) -> Result<Stats, String> {
        let mut stats = Stats::default();
        let mut present: HashSet<(String, String)> = HashSet::new();
        let mut orphans: HashSet<Relationship> = HashSet::new();
        let limit = usize::try_from(self.page_size).unwrap_or(usize::MAX);

        let mut cursor: Option<String> = None;
        loop {
            if stopping() {
                return Err("reconciliation stopped".to_owned());
            }
            let page = self.graph.read_page(
                self.resource_type,
                self.relation,
                cursor.as_deref(),
                self.page_size,
            )?;
            if page.relationships.is_empty() {
                break;
            }
            let ids: Vec<String> = page
                .relationships
                .iter()
                .map(|relationship| relationship.resource_id.clone())
                .collect();
            let subjects = self.source.subjects_of(&ids)?;
            for relationship in &page.relationships {
                stats.checked += 1;
                if relationship.subject_type == USER {
                    present.insert((
                        relationship.resource_id.clone(),
                        relationship.subject_id.clone(),
                    ));
                    if subjects.get(&relationship.resource_id) == Some(&relationship.subject_id) {
                        continue;
                    }
                }
                orphans.insert(relationship.clone());
                let now = (self.now)();
                let Some(first) = self.candidates.get(relationship).copied() else {
                    self.candidates.insert(relationship.clone(), now);
                    stats.orphans_pending += 1;
                    continue;
                };
                if now.saturating_duration_since(first) < self.grace {
                    stats.orphans_pending += 1;
                    continue;
                }
                self.graph
                    .write(RelationshipOperation::Delete, relationship)
                    .map_err(|reason| format!("delete {relationship}: {reason}"))?;
                self.candidates.remove(relationship);
                stats.orphans_deleted += 1;
            }
            if page.relationships.len() < limit || page.cursor.is_none() {
                break;
            }
            cursor = page.cursor;
        }
        // A candidate that is no longer orphan (its use case committed) is forgotten.
        self.candidates
            .retain(|relationship, _| orphans.contains(relationship));

        let mut after = String::new();
        loop {
            if stopping() {
                return Err("reconciliation stopped".to_owned());
            }
            let page = self.source.page(&after, limit)?;
            for (resource_id, subject_id) in &page {
                if present.contains(&(resource_id.clone(), subject_id.clone())) {
                    continue;
                }
                if !valid_object_id(resource_id) || !valid_object_id(subject_id) {
                    eprintln!(
                        "reconcile {}: {resource_id:?} of {subject_id:?} has an id SpiceDB cannot store",
                        self.name()
                    );
                    stats.unrepairable += 1;
                    continue;
                }
                let relationship = Relationship::new(
                    self.resource_type,
                    resource_id,
                    self.relation,
                    USER,
                    subject_id,
                );
                let revision = self
                    .graph
                    .write(RelationshipOperation::Touch, &relationship)
                    .map_err(|reason| format!("restore {relationship}: {reason}"))?;
                self.source
                    .record_revision(resource_id, subject_id, revision)
                    .map_err(|reason| format!("store revision of {relationship}: {reason}"))?;
                stats.missing_repaired += 1;
            }
            match page.last() {
                Some((last, _)) if page.len() >= limit => after.clone_from(last),
                _ => return Ok(stats),
            }
        }
    }
}

/// Schedule of the background loop.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct Schedule {
    /// Delay of the first pass after start.
    pub first_pass: Duration,
    /// Interval between passes.
    pub interval: Duration,
}

/// Runs passes of every reconciler until `stop` becomes `true`. A pass runs
/// on a blocking thread (the SpiceDB client and the store are synchronous);
/// a pass in progress at shutdown stops at the next page.
pub async fn run(
    mut reconcilers: Vec<Reconciler>,
    schedule: Schedule,
    mut stop: watch::Receiver<bool>,
) {
    let mut delay = schedule.first_pass;
    loop {
        tokio::select! {
            () = tokio::time::sleep(delay) => {}
            _ = stop.wait_for(|stopped| *stopped) => return,
        }
        let watcher = stop.clone();
        let pass = tokio::task::spawn_blocking(move || {
            let stopping = || *watcher.borrow();
            for reconciler in &mut reconcilers {
                match reconciler.reconcile(&stopping) {
                    Ok(stats) => println!(
                        "reconcile {}: checked={} orphans_pending={} orphans_deleted={} missing_repaired={} unrepairable={}",
                        reconciler.name(),
                        stats.checked,
                        stats.orphans_pending,
                        stats.orphans_deleted,
                        stats.missing_repaired,
                        stats.unrepairable
                    ),
                    Err(reason) => {
                        eprintln!("reconcile {}: pass failed: {reason}", reconciler.name())
                    }
                }
            }
            reconcilers
        });
        match pass.await {
            Ok(returned) => reconcilers = returned,
            Err(error) => {
                eprintln!("reconcile: pass aborted: {error}");
                return;
            }
        }
        if *stop.borrow() {
            return;
        }
        delay = schedule.interval;
    }
}

#[cfg(test)]
mod tests;

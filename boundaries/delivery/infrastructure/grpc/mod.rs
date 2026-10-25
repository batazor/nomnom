// Generated transport types stay inside the infrastructure boundary.
pub mod v1 {
    include!("gen/nomnom/delivery/v1/nomnom.delivery.v1.rs");
}

mod identity;
mod service;
mod tracing;

pub use identity::{
    CLOCK_LEEWAY_SECONDS, Caller, Clock, IdentityConfigError, MAX_USER_ASSERTION_TTL_SECONDS,
    ServiceTokenSubjectResolver, UserAssertionVerifier, subject_resolver,
};
pub use service::{GrpcWorker, RejectingSubjectResolver, SubjectResolver};
pub use tracing::{GrpcTraceLayer, TRACER_NAME, TracedService};

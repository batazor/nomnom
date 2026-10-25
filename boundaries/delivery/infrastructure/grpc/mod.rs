// Generated transport types stay inside the infrastructure boundary.
pub mod v1 {
    include!("gen/nomnom/delivery/v1/nomnom.delivery.v1.rs");
}

mod identity;
mod service;

pub use identity::{
    CLOCK_LEEWAY_SECONDS, IdentityConfigError, ServiceTokenSubjectResolver, subject_resolver,
};
pub use service::{GrpcWorker, RejectingSubjectResolver, SubjectResolver};

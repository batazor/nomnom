// Generated transport types stay inside the infrastructure boundary.
pub mod v1 {
    include!("gen/nomnom/delivery/v1/nomnom.delivery.v1.rs");
}

mod service;

pub use service::{GrpcWorker, RejectingSubjectResolver, SubjectResolver};

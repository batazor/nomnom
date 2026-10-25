"""gRPC transport adapter."""

from .identity import (
    IdentityConfigError,
    RejectingSubjectResolver,
    ServiceTokenSubjectResolver,
    SubjectResolver,
    UserAssertionVerifier,
    create_subject_resolver,
)
from .worker import GrpcWorker

__all__ = [
    "GrpcWorker",
    "IdentityConfigError",
    "RejectingSubjectResolver",
    "ServiceTokenSubjectResolver",
    "SubjectResolver",
    "UserAssertionVerifier",
    "create_subject_resolver",
]

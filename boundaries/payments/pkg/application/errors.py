"""Application errors shared by use cases of the payments boundary."""


class ApplicationError(Exception):
    """Base class of typed application errors mapped to transport codes by adapters."""


class PermissionDeniedError(ApplicationError):
    """The subject has no permission for the requested action on the resource."""

    def __init__(self, permission: str) -> None:
        super().__init__(f"permission denied: {permission}")
        self.permission = permission


class AuthorizationUnavailableError(ApplicationError):
    """The authorization check failed technically; protected use cases fail closed."""


class ConcurrencyConflictError(ApplicationError):
    """The aggregate version changed since it was loaded."""

    def __init__(self, aggregate_type: str, aggregate_id: str) -> None:
        super().__init__(f"concurrent modification of {aggregate_type} {aggregate_id}")
        self.aggregate_type = aggregate_type
        self.aggregate_id = aggregate_id

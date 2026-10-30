"""Authorization errors shared by use cases of the payments boundary."""

from pkg.usecase.errors import ApplicationError


class PermissionDeniedError(ApplicationError):
    """The subject has no permission for the requested action on the resource."""

    def __init__(self, permission: str) -> None:
        super().__init__(f"permission denied: {permission}")
        self.permission = permission


class AuthorizationUnavailableError(ApplicationError):
    """The authorization check failed technically; protected use cases fail closed."""

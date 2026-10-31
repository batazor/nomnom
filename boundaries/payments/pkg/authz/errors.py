"""Authorization errors shared by use cases of the payments boundary."""


class PermissionDeniedError(Exception):
    """The subject has no permission for the requested action on the resource."""

    def __init__(self, permission: str) -> None:
        super().__init__(f"permission denied: {permission}")
        self.permission = permission


class AuthorizationUnavailableError(Exception):
    """The authorization check failed technically; protected use cases fail closed."""

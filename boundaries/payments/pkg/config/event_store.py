"""Event store of the Payment streams (local ADR 2)."""

from pydantic import Field
from pydantic_settings import BaseSettings, SettingsConfigDict

EVENT_STORE_SNAPSHOT_EVERY_ENV = "EVENT_STORE__SNAPSHOT_EVERY"


class EventStoreSettings(BaseSettings):
    """Snapshot policy, required by the ``payments`` process.

    A commit passing a multiple of ``snapshot_every`` writes the snapshot of its Payment. A
    missing, empty or invalid value stops the process at startup.
    """

    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    snapshot_every: int = Field(ge=1, validation_alias=EVENT_STORE_SNAPSHOT_EVERY_ENV)

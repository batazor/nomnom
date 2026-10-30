"""Event store of the Payment streams (local ADR 2)."""

from pydantic import Field
from pydantic_settings import BaseSettings, SettingsConfigDict

EVENT_STORE_SNAPSHOT_EVERY_ENV = "EVENT_STORE__SNAPSHOT_EVERY"
EVENT_STORE_SUBJECT_KEK_ENV = "EVENT_STORE__SUBJECT_KEK"
# Ключи выводятся из KEK через HKDF: короткая строка не даёт нужной энтропии.
MIN_SUBJECT_KEK_LENGTH = 32


class EventStoreSettings(BaseSettings):
    """Snapshot policy and the KEK of per-user keys, required by the ``payments`` process.

    A commit passing a multiple of ``snapshot_every`` writes the snapshot of its Payment. A
    missing, empty or invalid value stops the process at startup.
    """

    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    snapshot_every: int = Field(ge=1, validation_alias=EVENT_STORE_SNAPSHOT_EVERY_ENV)
    # Приходит из Secret. Потеря KEK делает нечитаемыми все ключи субъектов.
    subject_kek: str = Field(
        min_length=MIN_SUBJECT_KEK_LENGTH, validation_alias=EVENT_STORE_SUBJECT_KEK_ENV, repr=False
    )

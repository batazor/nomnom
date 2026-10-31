"""Event store of the Payment streams (local ADR 2)."""

from pydantic import Field, field_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

EVENT_STORE_SNAPSHOT_EVERY_ENV = "EVENT_STORE__SNAPSHOT_EVERY"
EVENT_STORE_SUBJECT_KEKS_ENV = "EVENT_STORE__SUBJECT_KEKS"
EVENT_STORE_SUBJECT_REF_KEY_ENV = "EVENT_STORE__SUBJECT_REF_KEY"
# Ключи выводятся через HKDF, который не добавляет энтропии: значение должно быть случайным.
MIN_SUBJECT_SECRET_LENGTH = 32


class EventStoreSettings(BaseSettings):
    """Snapshot policy and the secrets of per-user keys, required by the ``payments`` process.

    A commit passing a multiple of ``snapshot_every`` writes the snapshot of its Payment. A
    missing, empty or invalid value stops the process at startup.
    """

    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    snapshot_every: int = Field(ge=1, validation_alias=EVENT_STORE_SNAPSHOT_EVERY_ENV)
    # JSON {"<версия>": "<KEK>"}: новые ключи оборачивает старшая версия, младшие нужны до
    # rewrap. Приходит из Secret; потеря версии делает нечитаемыми её ключи субъектов.
    subject_keks: dict[int, str] = Field(validation_alias=EVENT_STORE_SUBJECT_KEKS_ENV, repr=False)
    # HMAC поиска ключа пользователя: не меняется при ротации KEK, иначе пользователя не найти.
    subject_ref_key: str = Field(
        min_length=MIN_SUBJECT_SECRET_LENGTH,
        validation_alias=EVENT_STORE_SUBJECT_REF_KEY_ENV,
        repr=False,
    )

    @field_validator("subject_keks")
    @classmethod
    def _valid_keks(cls, value: dict[int, str]) -> dict[int, str]:
        if not value:
            raise ValueError(f"{EVENT_STORE_SUBJECT_KEKS_ENV} needs at least one version")
        for version, kek in value.items():
            if version < 1:
                raise ValueError(f"{EVENT_STORE_SUBJECT_KEKS_ENV} versions start at 1")
            if len(kek) < MIN_SUBJECT_SECRET_LENGTH:
                raise ValueError(
                    f"{EVENT_STORE_SUBJECT_KEKS_ENV} version {version} is shorter than "
                    f"{MIN_SUBJECT_SECRET_LENGTH} characters"
                )
        return value

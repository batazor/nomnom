"""Per-user keys of crypto-shredding (local ADR 2).

The stream holds only ``SealedSubject``; loading a Payment never touches this table. A data key
is wrapped by a key derived from a versioned KEK and bound by AES-GCM associated data to its
``key_id``; the row records that version, so a rotation only rewraps data keys and the sealed
identifiers stay valid. ``subject_ref`` comes from a separate secret: it must survive the rotation
of the KEK, otherwise a user could no longer be found and forgotten. Forgetting a user clears the
data key: every ciphertext of that user stays in the immutable stream, unreadable.
"""

import base64
import hashlib
import hmac
import os
import uuid
from collections.abc import Mapping
from dataclasses import dataclass

from cryptography.exceptions import InvalidTag
from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.ciphers.aead import AESGCM
from cryptography.hazmat.primitives.kdf.hkdf import HKDF

from infrastructure.repository.database import Database
from slices.payment.domains.payment import SealedSubject

from .event_codec import UnreadableStoredDataError

_KEY_BYTES = 32
_NONCE_BYTES = 12
_SALT = b"nomnom/payments/subject-keys"
_REWRAP_BATCH = 100


def _derive(secret: str, purpose: bytes) -> bytes:
    return HKDF(algorithm=hashes.SHA256(), length=_KEY_BYTES, salt=_SALT, info=purpose).derive(
        secret.encode()
    )


@dataclass(frozen=True)
class RewrapResult:
    """Keys moved to the newest KEK version and keys still on older versions."""

    rewrapped: int
    remaining: int


class DatabaseSubjectKeys:
    """``SubjectSealer`` over ``subject_keys``; inside a Unit of Work it joins the transaction."""

    def __init__(self, database: Database, keks: Mapping[int, str], ref_key: str) -> None:
        if not keks:
            raise ValueError("at least one KEK version is required")
        self._db = database
        self._wrappers = {
            version: AESGCM(_derive(kek, b"payments/subject-key-wrap/v1"))
            for version, kek in keks.items()
        }
        self._active_version = max(keks)
        self._ref_key = _derive(ref_key, b"payments/subject-ref/v1")

    def seal(self, subject_id: str) -> SealedSubject:
        """Encrypt the identifier under the key of the user, created on first use."""
        ref = self._ref(subject_id)
        candidate = str(uuid.uuid4())
        with self._db.connection() as conn:
            conn.execute(
                "INSERT INTO subject_keys (key_id, subject_ref, wrapped_key, kek_version, "
                "created_at) VALUES (%s, %s, %s, %s, now()) "
                "ON CONFLICT (subject_ref) DO NOTHING",
                (
                    candidate,
                    ref,
                    self._wrap(candidate, AESGCM.generate_key(_KEY_BYTES * 8)),
                    self._active_version,
                ),
            )
            row = conn.execute(
                "SELECT key_id, wrapped_key, kek_version FROM subject_keys WHERE subject_ref = %s",
                (ref,),
            ).fetchone()
        if row is None:
            raise UnreadableStoredDataError("subject key disappeared while sealing")
        key_id, wrapped, version = row
        nonce = os.urandom(_NONCE_BYTES)
        ciphertext = AESGCM(self._unwrap(key_id, bytes(wrapped), version)).encrypt(
            nonce, subject_id.encode(), key_id.encode()
        )
        return SealedSubject(key_id, _text(nonce), _text(ciphertext))

    def reveal(self, sealed: SealedSubject) -> str | None:
        """Return the identifier, or ``None`` when the user was forgotten.

        Raises:
            UnreadableStoredDataError: The key is unknown or the ciphertext does not match it.
        """
        with self._db.connection() as conn:
            row = conn.execute(
                "SELECT wrapped_key, kek_version FROM subject_keys WHERE key_id = %s",
                (sealed.key_id,),
            ).fetchone()
        if row is None:
            raise UnreadableStoredDataError(f"unknown subject key {sealed.key_id}")
        if row[0] is None:
            return None
        key = self._unwrap(sealed.key_id, bytes(row[0]), row[1])
        try:
            plain = AESGCM(key).decrypt(
                base64.b64decode(sealed.nonce),
                base64.b64decode(sealed.ciphertext),
                sealed.key_id.encode(),
            )
        except (InvalidTag, ValueError) as error:
            raise UnreadableStoredDataError(f"sealed subject of key {sealed.key_id}") from error
        return plain.decode()

    def forget(self, subject_id: str) -> bool:
        """Clear the key of the user; ``False`` when the user has no active key."""
        with self._db.connection() as conn:
            cursor = conn.execute(
                "UPDATE subject_keys SET subject_ref = NULL, wrapped_key = NULL, kek_version = NULL, "
                "forgotten_at = now() WHERE subject_ref = %s",
                (self._ref(subject_id),),
            )
            return cursor.rowcount > 0

    def rewrap(self) -> RewrapResult:
        """Move every active key to the newest KEK version, one committed batch at a time.

        ``remaining`` counts keys still on older versions: rows locked by a concurrent
        ``forget`` are skipped, and running the command again finishes them.

        Raises:
            UnreadableStoredDataError: A key is wrapped by a version missing from the config.
        """
        rewrapped = 0
        while True:
            moved = self._rewrap_batch()
            rewrapped += moved
            if moved == 0:
                break
        with self._db.connection() as conn:
            row = conn.execute(
                "SELECT count(*) FROM subject_keys WHERE kek_version < %s", (self._active_version,)
            ).fetchone()
        return RewrapResult(rewrapped, 0 if row is None else int(row[0]))

    def _rewrap_batch(self) -> int:
        with self._db.connection() as conn, conn.transaction():
            rows = conn.execute(
                "SELECT key_id, wrapped_key, kek_version FROM subject_keys "
                "WHERE kek_version < %s LIMIT %s FOR UPDATE SKIP LOCKED",
                (self._active_version, _REWRAP_BATCH),
            ).fetchall()
            for key_id, wrapped, version in rows:
                key = self._unwrap(key_id, bytes(wrapped), version)
                conn.execute(
                    "UPDATE subject_keys SET wrapped_key = %s, kek_version = %s WHERE key_id = %s",
                    (self._wrap(key_id, key), self._active_version, key_id),
                )
        return len(rows)

    def _ref(self, subject_id: str) -> bytes:
        return hmac.new(self._ref_key, subject_id.encode(), hashlib.sha256).digest()

    def _wrap(self, key_id: str, key: bytes) -> bytes:
        nonce = os.urandom(_NONCE_BYTES)
        wrapper = self._wrappers[self._active_version]
        return nonce + wrapper.encrypt(nonce, key, key_id.encode())

    def _unwrap(self, key_id: str, wrapped: bytes, version: int) -> bytes:
        wrapper = self._wrappers.get(version)
        if wrapper is None:
            raise UnreadableStoredDataError(
                f"subject key {key_id} needs KEK version {version}, which is not configured"
            )
        try:
            return wrapper.decrypt(wrapped[:_NONCE_BYTES], wrapped[_NONCE_BYTES:], key_id.encode())
        except InvalidTag as error:
            raise UnreadableStoredDataError(
                f"subject key {key_id} does not open with KEK version {version}"
            ) from error


def _text(value: bytes) -> str:
    return base64.b64encode(value).decode()

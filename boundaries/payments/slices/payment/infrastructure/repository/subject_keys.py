"""Per-user keys of crypto-shredding (local ADR 2).

The stream, the snapshots and the state of a Payment hold only ``SealedSubject``; loading a
Payment never touches this table. A data key is wrapped by a key derived from the KEK of the
environment and is bound by AES-GCM associated data to its ``key_id``. Forgetting a user clears
the data key: every ciphertext of that user stays in the immutable stream, unreadable.
"""

import base64
import hashlib
import hmac
import os
import uuid

from cryptography.exceptions import InvalidTag
from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.ciphers.aead import AESGCM
from cryptography.hazmat.primitives.kdf.hkdf import HKDF

from infrastructure.repository.database import Database
from slices.payment.domains.payment import SealedSubject

from .event_codec import UnreadableStoredDataError

_KEY_BYTES = 32
_NONCE_BYTES = 12


def _derive(kek: str, purpose: bytes) -> bytes:
    return HKDF(algorithm=hashes.SHA256(), length=_KEY_BYTES, salt=None, info=purpose).derive(
        kek.encode()
    )


class DatabaseSubjectKeys:
    """``SubjectSealer`` over ``subject_keys``; inside a Unit of Work it joins the transaction."""

    def __init__(self, database: Database, kek: str) -> None:
        self._db = database
        self._wrapping = AESGCM(_derive(kek, b"payments/subject-key-wrap/v1"))
        self._ref_key = _derive(kek, b"payments/subject-ref/v1")

    def seal(self, subject_id: str) -> SealedSubject:
        """Encrypt the identifier under the key of the user, created on first use."""
        ref = self._ref(subject_id)
        candidate = str(uuid.uuid4())
        with self._db.connection() as conn:
            conn.execute(
                "INSERT INTO subject_keys (key_id, subject_ref, wrapped_key, created_at) "
                "VALUES (%s, %s, %s, now()) ON CONFLICT (subject_ref) DO NOTHING",
                (candidate, ref, self._wrap(candidate, AESGCM.generate_key(_KEY_BYTES * 8))),
            )
            row = conn.execute(
                "SELECT key_id, wrapped_key FROM subject_keys WHERE subject_ref = %s", (ref,)
            ).fetchone()
        if row is None:
            raise UnreadableStoredDataError("subject key disappeared while sealing")
        key_id, wrapped = row
        nonce = os.urandom(_NONCE_BYTES)
        ciphertext = AESGCM(self._unwrap(key_id, bytes(wrapped))).encrypt(
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
                "SELECT wrapped_key FROM subject_keys WHERE key_id = %s", (sealed.key_id,)
            ).fetchone()
        if row is None:
            raise UnreadableStoredDataError(f"unknown subject key {sealed.key_id}")
        if row[0] is None:
            return None
        key = self._unwrap(sealed.key_id, bytes(row[0]))
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
                "UPDATE subject_keys SET subject_ref = NULL, wrapped_key = NULL, "
                "forgotten_at = now() WHERE subject_ref = %s",
                (self._ref(subject_id),),
            )
            return cursor.rowcount > 0

    def _ref(self, subject_id: str) -> bytes:
        return hmac.new(self._ref_key, subject_id.encode(), hashlib.sha256).digest()

    def _wrap(self, key_id: str, key: bytes) -> bytes:
        nonce = os.urandom(_NONCE_BYTES)
        return nonce + self._wrapping.encrypt(nonce, key, key_id.encode())

    def _unwrap(self, key_id: str, wrapped: bytes) -> bytes:
        try:
            return self._wrapping.decrypt(
                wrapped[:_NONCE_BYTES], wrapped[_NONCE_BYTES:], key_id.encode()
            )
        except InvalidTag as error:
            raise UnreadableStoredDataError(
                f"subject key {key_id} does not open with this KEK"
            ) from error


def _text(value: bytes) -> str:
    return base64.b64encode(value).decode()

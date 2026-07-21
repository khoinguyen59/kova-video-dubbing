"""Small SQLite persistence layer for immutable, consented voice profiles."""

from __future__ import annotations

from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
import sqlite3
from threading import RLock
from uuid import uuid4


def utc_now() -> str:
    return datetime.now(UTC).isoformat()


@dataclass(frozen=True)
class Profile:
    id: str
    name: str
    language: str
    status: str
    created_at: str


@dataclass(frozen=True)
class ProfileVersion:
    id: str
    profile_id: str
    version: int
    reference_filename: str
    reference_sha256: str
    reference_path: str
    reference_duration_seconds: float
    engine: str
    engine_version: str
    consent_at: str
    created_at: str


class ProfileStore:
    def __init__(self, database_path: str | Path) -> None:
        self._path = Path(database_path)
        self._path.parent.mkdir(parents=True, exist_ok=True)
        self._lock = RLock()
        self._connection = sqlite3.connect(self._path, check_same_thread=False)
        self._connection.row_factory = sqlite3.Row
        self._migrate()

    def close(self) -> None:
        with self._lock:
            self._connection.close()

    def _migrate(self) -> None:
        self._connection.executescript(
            """
            PRAGMA foreign_keys = ON;
            CREATE TABLE IF NOT EXISTS profiles (
              id TEXT PRIMARY KEY,
              name TEXT NOT NULL,
              language TEXT NOT NULL,
              status TEXT NOT NULL,
              created_at TEXT NOT NULL
            );
            CREATE TABLE IF NOT EXISTS profile_versions (
              id TEXT PRIMARY KEY,
              profile_id TEXT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
              version INTEGER NOT NULL,
              reference_filename TEXT NOT NULL,
              reference_sha256 TEXT NOT NULL,
              reference_path TEXT NOT NULL DEFAULT '',
              reference_duration_seconds REAL NOT NULL DEFAULT 0,
              engine TEXT NOT NULL,
              engine_version TEXT NOT NULL,
              consent_at TEXT NOT NULL,
              created_at TEXT NOT NULL,
              UNIQUE(profile_id, version)
            );
            """
        )
        self._connection.commit()

        # The first development builds did not persist the worker-owned path.
        # Keep this migration idempotent so a profile database is never lost.
        columns = {row[1] for row in self._connection.execute("PRAGMA table_info(profile_versions)")}
        if "reference_path" not in columns:
            self._connection.execute("ALTER TABLE profile_versions ADD COLUMN reference_path TEXT NOT NULL DEFAULT ''")
            self._connection.commit()
        if "reference_duration_seconds" not in columns:
            self._connection.execute("ALTER TABLE profile_versions ADD COLUMN reference_duration_seconds REAL NOT NULL DEFAULT 0")
            self._connection.commit()

    def create_profile(
        self,
        *,
        name: str,
        language: str,
        reference_filename: str,
        reference_sha256: str,
        reference_path: str = "",
        reference_duration_seconds: float = 0,
        consent: bool,
        engine: str = "omnivoice",
        engine_version: str = "pending",
    ) -> tuple[Profile, ProfileVersion]:
        if not consent:
            raise ValueError("voice consent is required")
        if not name.strip() or not reference_filename.strip() or len(reference_sha256.strip()) != 64 or reference_duration_seconds < 0:
            raise ValueError("profile name, reference filename, and SHA-256 are required")
        now = utc_now()
        profile = Profile(uuid4().hex, name.strip(), language.strip() or "vi", "ready", now)
        version = ProfileVersion(
            uuid4().hex,
            profile.id,
            1,
            reference_filename.strip(),
            reference_sha256.lower().strip(),
            reference_path.strip(),
            float(reference_duration_seconds),
            engine.strip() or "omnivoice",
            engine_version.strip() or "pending",
            now,
            now,
        )
        with self._lock, self._connection:
            self._connection.execute(
                "INSERT INTO profiles(id, name, language, status, created_at) VALUES (?, ?, ?, ?, ?)",
                (profile.id, profile.name, profile.language, profile.status, profile.created_at),
            )
            self._connection.execute(
                """INSERT INTO profile_versions(id, profile_id, version, reference_filename,
                   reference_sha256, reference_path, reference_duration_seconds, engine, engine_version, consent_at, created_at)
                   VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
                (
                    version.id,
                    version.profile_id,
                    version.version,
                    version.reference_filename,
                    version.reference_sha256,
                    version.reference_path,
                    version.reference_duration_seconds,
                    version.engine,
                    version.engine_version,
                    version.consent_at,
                    version.created_at,
                ),
            )
        return profile, version

    def set_reference_path(self, version_id: str, reference_path: str) -> None:
        with self._lock, self._connection:
            self._connection.execute(
                "UPDATE profile_versions SET reference_path = ? WHERE id = ?",
                (reference_path, version_id),
            )

    def list_profiles(self) -> list[Profile]:
        with self._lock:
            rows = self._connection.execute(
                "SELECT id, name, language, status, created_at FROM profiles ORDER BY created_at DESC"
            ).fetchall()
        return [Profile(**dict(row)) for row in rows]

    def get_profile(self, profile_id: str) -> Profile | None:
        with self._lock:
            row = self._connection.execute(
                "SELECT id, name, language, status, created_at FROM profiles WHERE id = ?", (profile_id,)
            ).fetchone()
        return Profile(**dict(row)) if row else None

    def latest_version(self, profile_id: str) -> ProfileVersion | None:
        with self._lock:
            row = self._connection.execute(
                """SELECT id, profile_id, version, reference_filename, reference_sha256, reference_path, reference_duration_seconds, engine,
                   engine_version, consent_at, created_at FROM profile_versions
                   WHERE profile_id = ? ORDER BY version DESC LIMIT 1""",
                (profile_id,),
            ).fetchone()
        return ProfileVersion(**dict(row)) if row else None

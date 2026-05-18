# Testing

## Storage tests

Lightweight storage and migration smoke tests may use in-memory SQLite through
GORM. These tests are intended to catch model registration, repository wiring,
and basic service behavior without requiring a local MySQL server.

SQLite tests are not a replacement for MySQL coverage. Any behavior that depends
on MySQL-specific SQL, indexes, locking, JSON semantics, unsigned integers, or
transaction isolation should be covered later with a real MySQL-backed
integration test once the domain repositories exist.

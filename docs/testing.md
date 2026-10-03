# Testing

Run everything from `apps/backend`:

```sh
gofmt -l .
go vet ./...
go test ./...
```

While working, run the narrowest package first, for example
`go test ./internal/quack -run Escalation`, then the whole suite.
`go test -race ./...` is worth running after touching the worker, router, or
anything with locks or leases. `staticcheck ./...` is optional.

## How the tests are built

- **Storage.** Most tests use a real `store.Store` over in-memory SQLite with
  the full schema migrated, plus miniredis when Redis matters. The helpers are
  in `internal/testutil/store.go`. The SQLite database holds a single
  connection, so code that opens a second query outside its own transaction
  hangs the test instead of passing by accident.
- **Discord.** Tests swap in fakes for the `quack` Discord ports and the
  router's interaction client; no test talks to Discord. The `/case` command
  definitions and rendered messages are pinned by golden files in
  `internal/discord/testdata/`. Their custom IDs are part of messages already
  posted in Discord, so an unexpected diff there is a real break.
- **HTTP.** `internal/api` tests drive the real `api.Server` with
  `httptest`. They pin the JSON contract the dashboard uses: routes, status
  codes, the error envelope, cookies, CSRF, and idempotency.
- **Config.** `internal/config` tests pass the environment in explicitly, so
  they don't depend on your shell.
- **Fuzz.** `internal/quack/fuzz_test.go` has fuzz targets for template
  policy JSON and context values (`go test ./internal/quack -fuzz <name>`).

## MySQL tests

SQLite does not behave like MySQL for locking, generated columns, or the
migration lock. A few tests need a real MySQL and skip unless
`QUACK_TEST_MYSQL_DSN` is set:

- `TestMySQL` in `internal/store/migrate_mysql_test.go`: concurrent
  migration under the named lock, the default-level index, rollback, and the
  raw SQL behind polling and statistics.
- `TestMySQL*` in `internal/quack/mysql_integration_test.go`: concurrent case
  creation and escalation under the guild lock.

Each test creates and drops its own uniquely named database, so the DSN's
user needs `CREATE` and `DROP` privileges. With the Compose MySQL, use root:

```sh
QUACK_TEST_MYSQL_DSN='root:quack-root@tcp(127.0.0.1:3306)/?parseTime=true' \
  go test ./internal/store ./internal/quack -run MySQL
```

CI does not set this variable, so these tests only run locally.

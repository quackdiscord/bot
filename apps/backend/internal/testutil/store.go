package testutil

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/quackdiscord/bot/internal/store"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// NewSQLiteDB returns a private in-memory SQLite database with the full
// schema migrated. It holds a single connection, which keeps the in-memory
// database alive and serializes transactions the way MySQL row locks would.
// Code that opens a second query outside its own transaction will block, so
// that bug shows up as a hung test.
func NewSQLiteDB(t testing.TB) *gorm.DB {
	t.Helper()
	// Tests provoke constraint violations on purpose; GORM's default logger
	// would print each one.
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sqlite handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := store.New(db, nil).Migrate(); err != nil {
		t.Fatalf("migrate sqlite test database: %v", err)
	}
	return db
}

// NewSQLiteStore returns a Store over NewSQLiteDB, without Redis.
func NewSQLiteStore(t testing.TB) *store.Store {
	t.Helper()
	return store.New(NewSQLiteDB(t), nil)
}

// NewSQLiteRedisStore returns a Store over NewSQLiteDB and an in-process
// Redis.
func NewSQLiteRedisStore(t testing.TB) *store.Store {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return store.New(NewSQLiteDB(t), client)
}

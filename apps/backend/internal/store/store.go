// Package store implements the quack storage ports on MySQL (through GORM)
// and Redis.
//
// Every moderation change commits in one transaction with its timeline events
// and audit entries. Cases and audit entries are never deleted by normal
// operations. Action and notification leases fence stale workers, but a lease
// cannot tell whether a Discord request that was in flight succeeded, so an
// expired lease always ends in staff review rather than a blind retry.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	mysqlconfig "github.com/go-sql-driver/mysql"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/redis/go-redis/v9"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// leaseDuration is how long a worker owns a claimed action or notification
// before another worker may treat it as abandoned.
const leaseDuration = 2 * time.Minute

// errNoRedis is returned by Redis-backed methods on a Store built without
// Redis.
var errNoRedis = errors.New("redis not connected")

// Store implements every quack storage port. It is safe for concurrent use.
type Store struct {
	db    *gorm.DB
	redis *redis.Client

	// pollMu guards pollCursor, the guild ListExecutableCaseIDs served last.
	pollMu     sync.Mutex
	pollCursor string
}

var (
	_ quack.Store                   = (*Store)(nil)
	_ quack.GuildStore              = (*Store)(nil)
	_ quack.SettingsStore           = (*Store)(nil)
	_ quack.TemplateStore           = (*Store)(nil)
	_ quack.CaseStore               = (*Store)(nil)
	_ quack.ActionStore             = (*Store)(nil)
	_ quack.EvidenceStore           = (*Store)(nil)
	_ quack.AppealStore             = (*Store)(nil)
	_ quack.AppealNotificationStore = (*Store)(nil)
	_ quack.AuditStore              = (*Store)(nil)
	_ quack.AuditMirrorStore        = (*Store)(nil)
	_ quack.StatisticsStore         = (*Store)(nil)
	_ quack.OpsStore                = (*Store)(nil)
)

// New returns a Store over db and redis. redis may be nil for tools that only
// touch MySQL, such as migrations and the v4 importer; the session methods
// and PingRedis then fail.
func New(db *gorm.DB, redis *redis.Client) *Store {
	installAuditImmutability(db)
	return &Store{db: db, redis: redis}
}

// OpenMySQL connects to MySQL and pings it, so startup fails fast when the
// database is unreachable.
func OpenMySQL(dsn string) (*gorm.DB, error) {
	normalized, err := normalizeMySQLDSN(dsn)
	if err != nil {
		return nil, err
	}
	db, err := gorm.Open(gormmysql.Open(normalized), &gorm.Config{Logger: dbLogger{level: logger.Info}})
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql database: %w", err)
	}
	sqlDB.SetConnMaxLifetime(5 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return db, nil
}

// normalizeMySQLDSN accepts a go-sql-driver DSN or a mysql:// URL, such as
// the DATABASE_URL hosting platforms hand out, and turns on parseTime, which
// the records' time fields need.
func normalizeMySQLDSN(dsn string) (string, error) {
	var cfg *mysqlconfig.Config
	var err error
	if strings.HasPrefix(dsn, "mysql://") {
		cfg, err = parseMySQLURL(dsn)
	} else {
		cfg, err = mysqlconfig.ParseDSN(dsn)
	}
	if err != nil {
		return "", fmt.Errorf("parse mysql dsn: %w", err)
	}
	cfg.ParseTime = true
	return cfg.FormatDSN(), nil
}

// parseMySQLURL reads mysql://user:pass@host[:port]/db?params. The query
// takes the same parameters as a DSN.
func parseMySQLURL(raw string) (*mysqlconfig.Config, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Host == "" {
		return nil, errors.New("mysql url has no host")
	}
	cfg, err := mysqlconfig.ParseDSN("/?" + u.RawQuery)
	if err != nil {
		return nil, err
	}
	cfg.User = u.User.Username()
	cfg.Passwd, _ = u.User.Password()
	cfg.Net = "tcp"
	cfg.Addr = u.Host
	if u.Port() == "" {
		cfg.Addr = net.JoinHostPort(u.Hostname(), "3306")
	}
	cfg.DBName = strings.TrimPrefix(u.Path, "/")
	return cfg, nil
}

// OpenRedis connects to Redis and pings it.
func OpenRedis(rawURL string) (*redis.Client, error) {
	options, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse Redis URL: %w", err)
	}
	client := redis.NewClient(options)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping Redis: %w", err)
	}
	return client, nil
}

// DB returns the GORM handle. Module stores share it; nothing else should.
func (s *Store) DB() *gorm.DB { return s.db }

// Redis returns the Redis client for interaction dedupe and idempotency keys.
func (s *Store) Redis() *redis.Client { return s.redis }

// PingDatabase reports whether MySQL is reachable.
func (s *Store) PingDatabase(ctx context.Context) error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

// PingRedis reports whether Redis is reachable.
func (s *Store) PingRedis(ctx context.Context) error {
	if s.redis == nil {
		return errNoRedis
	}
	return s.redis.Ping(ctx).Err()
}

// WithGuildCaseLock runs fn in a transaction that holds the guild row lock.
// Case numbering and escalation counts read and write under it, so two cases
// for one guild never race.
func (s *Store) WithGuildCaseLock(ctx context.Context, guildID string, fn func(quack.CaseStore) error) error {
	if guildID == "" {
		return errors.New("guild id is required")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var guild guildRecord
		found, err := first(forUpdate(tx).Where("id = ?", guildID), &guild)
		if err != nil {
			return fmt.Errorf("lock guild for case creation: %w", err)
		}
		if !found {
			return errors.New("guild not found")
		}
		return fn(&Store{db: tx, redis: s.redis})
	})
}

// Missing rows follow one pattern. Lookups go through first, which reports a
// missing row as found == false rather than an error, or findOne, which
// returns nil for it. Port methods return (nil, nil) for a missing row. A
// transaction that finds its target missing returns errNotFound, which
// notFoundIsNil turns back into (nil, nil).

// errNotFound aborts a transaction whose target row is missing.
var errNotFound = errors.New("not found")

// first loads the first row of query into dest and reports whether there was
// one.
func first(query *gorm.DB, dest any) (bool, error) {
	result := query.Limit(1).Find(dest)
	return result.RowsAffected > 0, result.Error
}

// findOne loads the first row of query and maps it with model, or returns
// nil when there is none. op names the lookup in the error.
func findOne[R, M any](query *gorm.DB, op string, model func(R) M) (*M, error) {
	var record R
	found, err := first(query, &record)
	if err != nil || !found {
		return nil, wrap(op, err)
	}
	m := model(record)
	return &m, nil
}

// modelsOf maps records to domain values with model.
func modelsOf[R, M any](records []R, model func(R) M) []M {
	out := make([]M, len(records))
	for i, r := range records {
		out[i] = model(r)
	}
	return out
}

// ulid rebuilds the domain identity block from a record's columns.
func ulid(id string, createdAt, updatedAt time.Time) quack.ULIDModel {
	return quack.ULIDModel{ID: id, CreatedAt: createdAt, UpdatedAt: updatedAt}
}

// stamp gives a new row its ID and timestamps, keeping any the caller set.
func stamp(m *quack.ULIDModel, now time.Time) {
	if m.ID == "" {
		m.ID = quack.NewID()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
}

// notFoundIsNil turns errNotFound into a nil error.
func notFoundIsNil(err error) error {
	if errors.Is(err, errNotFound) {
		return nil
	}
	return err
}

// wrap adds context to err, passing nil through, so a lookup can return
// wrap(op, err) whether or not it failed.
func wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", op, err)
}

// forUpdate adds SELECT ... FOR UPDATE. SQLite ignores it and serializes
// writers on its own.
func forUpdate(tx *gorm.DB) *gorm.DB {
	return tx.Clauses(clause.Locking{Strength: "UPDATE"})
}

// page clamps a requested page to 1..100 rows, defaulting to 50, and a
// non-negative offset.
func page(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 50
	}
	return min(limit, 100), max(offset, 0)
}

// isDuplicate reports whether err is a unique-constraint violation on either
// MySQL or SQLite.
func isDuplicate(err error) bool {
	var mysqlErr *mysqlconfig.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == 1062
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "duplicate") || strings.Contains(text, "unique constraint")
}

// jsonObject encodes value for a metadata column, falling back to an empty
// object; metadata is diagnostic and never worth failing a write over.
func jsonObject(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(body)
}

// dbLogger sends GORM diagnostics to slog without SQL text, bound values, or
// driver messages, any of which can carry moderation content.
type dbLogger struct{ level logger.LogLevel }

func (l dbLogger) LogMode(level logger.LogLevel) logger.Interface {
	l.level = level
	return l
}

func (l dbLogger) Info(ctx context.Context, _ string, _ ...any) {
	if l.level >= logger.Info {
		slog.DebugContext(ctx, "Database event")
	}
}

func (l dbLogger) Warn(ctx context.Context, _ string, _ ...any) {
	if l.level >= logger.Warn {
		slog.WarnContext(ctx, "Database warning")
	}
}

func (l dbLogger) Error(ctx context.Context, _ string, _ ...any) {
	if l.level >= logger.Error {
		slog.ErrorContext(ctx, "Database operation failed")
	}
}

// Trace logs failed and slow queries by operation and duration. Fast,
// successful queries are not logged at all: the background pollers run
// several every second and would drown out everything else.
func (l dbLogger) Trace(ctx context.Context, begin time.Time, query func() (string, int64), err error) {
	if l.level == logger.Silent || errors.Is(err, gorm.ErrRecordNotFound) {
		return
	}
	elapsed := time.Since(begin)
	var level slog.Level
	switch {
	case err != nil && l.level >= logger.Error:
		level = slog.LevelError
	case elapsed >= 200*time.Millisecond && l.level >= logger.Warn:
		level = slog.LevelWarn
	default:
		return
	}
	if !slog.Default().Enabled(ctx, level) {
		return
	}
	sql, rows := query()
	operation := "query"
	if words := strings.Fields(sql); len(words) > 0 {
		switch word := strings.ToUpper(words[0]); word {
		case "SELECT", "INSERT", "UPDATE", "DELETE", "CREATE", "ALTER", "DROP":
			operation = word
		}
	}
	slog.Log(ctx, level, "Database query completed", "operation", operation,
		"duration", elapsed, "rows", rows, "error_type", fmt.Sprintf("%T", err))
}

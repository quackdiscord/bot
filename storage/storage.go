package storage

import (
	r "github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// Storage is the data access layer. It holds connections to all backing stores
// and exposes methods for every data operation.
type Store struct {
	db    *gorm.DB
	redis *r.Client
}

func (s *Store) Redis() *r.Client {
	return s.redis
}

func (s *Store) DB() *gorm.DB {
	return s.db
}

func New(db *gorm.DB, redis *r.Client) *Store {
	return &Store{db: db, redis: redis}
}

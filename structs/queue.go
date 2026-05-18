package structs

import (
	r "github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type DataStore interface {
	DB() *gorm.DB
	Redis() *r.Client
}

type QueueEvent struct {
	Type    string
	Data    any
	Handler func(s DataStore, data any)
}

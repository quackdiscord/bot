package storage

import (
	"errors"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/structs"
	"gorm.io/gorm"
)

const mysqlTableOptions = "ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci"

type migration struct {
	Name  string
	Apply func(tx *gorm.DB) error
}

type schemaMigration struct {
	Name      string    `gorm:"size:191;primaryKey"`
	AppliedAt time.Time `gorm:"not null;index"`
}

func (schemaMigration) TableName() string {
	return "schema_migrations"
}

func (s *Store) Migrate() error {
	if s == nil || s.db == nil {
		return errors.New("database not connected")
	}

	if err := withMySQLTableOptions(s.db).AutoMigrate(&schemaMigration{}); err != nil {
		return fmt.Errorf("migrate schema migrations table: %w", err)
	}

	migrations := []migration{
		{
			Name: "0001_v5_schema",
			Apply: func(tx *gorm.DB) error {
				models := structs.SchemaModels()
				if len(models) == 0 {
					return errors.New("no schema models registered")
				}

				return withMySQLTableOptions(tx).AutoMigrate(models...)
			},
		},
	}

	for _, m := range migrations {
		var existing int64
		if err := s.db.Model(&schemaMigration{}).Where("name = ?", m.Name).Count(&existing).Error; err != nil {
			return fmt.Errorf("check migration %s: %w", m.Name, err)
		}

		if existing > 0 {
			continue
		}

		err := s.db.Transaction(func(tx *gorm.DB) error {
			if err := m.Apply(tx); err != nil {
				return err
			}

			record := &schemaMigration{
				Name:      m.Name,
				AppliedAt: time.Now().UTC(),
			}

			if err := tx.Create(record).Error; err != nil {
				return fmt.Errorf("record migration %s: %w", m.Name, err)
			}

			return nil
		})
		if err != nil {
			return fmt.Errorf("apply migration %s: %w", m.Name, err)
		}
	}

	return nil
}

func withMySQLTableOptions(db *gorm.DB) *gorm.DB {
	if db.Dialector.Name() != "mysql" {
		return db
	}

	return db.Set("gorm:table_options", mysqlTableOptions)
}

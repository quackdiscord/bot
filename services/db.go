package services

import (
	"errors"
	"fmt"

	mysqlconfig "github.com/go-sql-driver/mysql"
	"github.com/quackdiscord/bot/lib"
	"github.com/rs/zerolog/log"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type dbService struct {
	*gorm.DB
}

var DB = &dbService{}

func (s *dbService) Connect() {
	dsn, err := normalizeMySQLDSN(lib.Config.Storage.DBDSN)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to parse database DSN")
	}

	db, err := gorm.Open(gormmysql.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to database")
	}

	sqlDB, err := db.DB()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to get sql.DB from gorm")
	}

	if err := sqlDB.Ping(); err != nil {
		log.Fatal().Err(err).Msg("Failed to ping database")
	}

	s.DB = db
	log.Info().Msg("Connected to database")
}

func (s *dbService) Ping() error {
	if s.DB == nil {
		return errors.New("database not connected")
	}
	sqlDB, err := s.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Ping()
}

func normalizeMySQLDSN(dsn string) (string, error) {
	cfg, err := mysqlconfig.ParseDSN(dsn)
	if err != nil {
		return "", fmt.Errorf("parse mysql dsn: %w", err)
	}

	cfg.ParseTime = true

	return cfg.FormatDSN(), nil
}

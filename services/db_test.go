package services

import (
	"testing"

	mysqlconfig "github.com/go-sql-driver/mysql"
)

func TestNormalizeMySQLDSNEnablesParseTime(t *testing.T) {
	dsn, err := normalizeMySQLDSN("user:pass@tcp(localhost:3306)/quack")
	if err != nil {
		t.Fatalf("normalize dsn: %v", err)
	}

	cfg, err := mysqlconfig.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse normalized dsn: %v", err)
	}

	if !cfg.ParseTime {
		t.Fatalf("expected parseTime to be enabled")
	}
}

func TestNormalizeMySQLDSNPreservesDSNFields(t *testing.T) {
	dsn, err := normalizeMySQLDSN("user:pass@tcp(localhost:3306)/quack?loc=Local&timeout=5s")
	if err != nil {
		t.Fatalf("normalize dsn: %v", err)
	}

	cfg, err := mysqlconfig.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse normalized dsn: %v", err)
	}

	if !cfg.ParseTime {
		t.Fatalf("expected parseTime to be enabled")
	}
	if cfg.User != "user" || cfg.Passwd != "pass" || cfg.Net != "tcp" || cfg.Addr != "localhost:3306" || cfg.DBName != "quack" {
		t.Fatalf("expected connection fields to be preserved, got %+v", cfg)
	}
	if cfg.Loc.String() != "Local" {
		t.Fatalf("expected loc to be preserved, got %s", cfg.Loc.String())
	}
	if cfg.Timeout.String() != "5s" {
		t.Fatalf("expected timeout to be preserved, got %s", cfg.Timeout.String())
	}
}

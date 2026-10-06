package store

import (
	"errors"
	"testing"

	mysqlconfig "github.com/go-sql-driver/mysql"
)

func TestNormalizeMySQLDSN(t *testing.T) {
	dsn, err := normalizeMySQLDSN("user:pass@tcp(localhost:3306)/quack?loc=Local&timeout=5s")
	if err != nil {
		t.Fatalf("normalize dsn: %v", err)
	}
	cfg, err := mysqlconfig.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse normalized dsn: %v", err)
	}
	if !cfg.ParseTime {
		t.Error("parseTime is off")
	}
	if cfg.User != "user" || cfg.Passwd != "pass" || cfg.Addr != "localhost:3306" || cfg.DBName != "quack" ||
		cfg.Loc.String() != "Local" || cfg.Timeout.String() != "5s" {
		t.Errorf("normalization lost fields: %+v", cfg)
	}
}

func TestNormalizeMySQLURL(t *testing.T) {
	tests := []struct {
		url, addr string
	}{
		{"mysql://root:p%40ss@mysql:3306/app?timeout=5s", "mysql:3306"},
		{"mysql://root:p%40ss@mysql/app?timeout=5s", "mysql:3306"},
	}
	for _, test := range tests {
		dsn, err := normalizeMySQLDSN(test.url)
		if err != nil {
			t.Fatalf("normalize %q: %v", test.url, err)
		}
		cfg, err := mysqlconfig.ParseDSN(dsn)
		if err != nil {
			t.Fatalf("parse normalized dsn: %v", err)
		}
		if cfg.User != "root" || cfg.Passwd != "p@ss" || cfg.Net != "tcp" || cfg.Addr != test.addr ||
			cfg.DBName != "app" || cfg.Timeout.String() != "5s" || !cfg.ParseTime {
			t.Errorf("normalize %q = %+v", test.url, cfg)
		}
	}
	for _, bad := range []string{"mysql:///app", "mysql://root@mysql/app?timeout=soon"} {
		if _, err := normalizeMySQLDSN(bad); err == nil {
			t.Errorf("normalize %q succeeded, want an error", bad)
		}
	}
}

func TestPage(t *testing.T) {
	tests := []struct {
		limit, offset         int
		wantLimit, wantOffset int
	}{
		{0, 0, 50, 0},
		{-5, -5, 50, 0},
		{20, 40, 20, 40},
		{500, 0, 100, 0},
	}
	for _, test := range tests {
		limit, offset := page(test.limit, test.offset)
		if limit != test.wantLimit || offset != test.wantOffset {
			t.Errorf("page(%d, %d) = %d, %d; want %d, %d", test.limit, test.offset, limit, offset, test.wantLimit, test.wantOffset)
		}
	}
}

func TestIsDuplicate(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{&mysqlconfig.MySQLError{Number: 1062, Message: "Duplicate entry"}, true},
		{&mysqlconfig.MySQLError{Number: 1146, Message: "Table doesn't exist"}, false},
		{errors.New("UNIQUE constraint failed: appeals.case_id"), true},
		{errors.New("connection refused"), false},
	}
	for _, test := range tests {
		if got := isDuplicate(test.err); got != test.want {
			t.Errorf("isDuplicate(%v) = %v, want %v", test.err, got, test.want)
		}
	}
}

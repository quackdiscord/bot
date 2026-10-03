package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"strings"
	"testing"
)

func TestRunRejectsBadUsage(t *testing.T) {
	// None of these may reach config loading or the database.
	tests := [][]string{
		{"frobnicate"},
		{"serve", "extra"},
		{"serve", "-nope"},
		{"migrate", "sideways"},
		{"migrate", "up", "down"},
		{"import-v4"},
		{"import-v4", "export"},
		{"import-v4", "import"},
		{"import-v4", "rollback", "-guild", "g"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stderr bytes.Buffer
			err := run(context.Background(), args, &bytes.Buffer{}, &stderr)
			if !errors.Is(err, errReported) {
				t.Fatalf("run(%q) = %v, want errReported", args, err)
			}
			if stderr.Len() == 0 {
				t.Error("no explanation printed")
			}
		})
	}
}

func TestRunHelp(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		var stdout bytes.Buffer
		if err := run(context.Background(), []string{arg}, &stdout, &bytes.Buffer{}); err != nil {
			t.Fatalf("%s: %v", arg, err)
		}
		if !strings.Contains(stdout.String(), "import-v4") {
			t.Errorf("%s: usage missing commands:\n%s", arg, stdout.String())
		}
	}
	err := run(context.Background(), []string{"migrate", "-h"}, &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, flag.ErrHelp) {
		t.Errorf("migrate -h = %v, want flag.ErrHelp", err)
	}
}

func TestCheckScope(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{name: "collision", args: []string{"-v4", "case,warn", "-v5", "case"}, wantErr: true},
		{name: "direct commands after migration", args: []string{"-v4", "warn", "-v5", "case", "-after-migration"}, wantErr: true},
		{name: "isolated", args: []string{"-v4", "ticket", "-v5", "case"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"import-v4", "check-scope"}, test.args...)
			err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{})
			if (err != nil) != test.wantErr {
				t.Fatalf("check-scope %q = %v, want error %v", test.args, err, test.wantErr)
			}
		})
	}
}

func TestMigrateRequiresDSN(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("QUACK_CONFIG", "")
	t.Setenv("QUACK_DATABASE_DSN", "")
	err := run(context.Background(), []string{"migrate"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "database.dsn") {
		t.Fatalf("migrate without a DSN = %v, want a database.dsn error", err)
	}
}

func TestServeReportsEveryConfigProblem(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("QUACK_CONFIG", "")
	for _, name := range []string{"QUACK_DATABASE_DSN", "QUACK_REDIS_URL", "QUACK_DISCORD_TOKEN"} {
		t.Setenv(name, "")
	}
	var stderr bytes.Buffer
	err := run(context.Background(), nil, &bytes.Buffer{}, &stderr)
	if !errors.Is(err, errReported) {
		t.Fatalf("serve with an empty config = %v, want errReported", err)
	}
	for _, key := range []string{"database.dsn", "redis.url", "discord.token"} {
		if !strings.Contains(stderr.String(), key) {
			t.Errorf("stderr does not mention %s:\n%s", key, stderr.String())
		}
	}
}

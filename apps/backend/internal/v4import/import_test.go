package v4import

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// validRow is one importable row for guild "guild".
const validRow = `{"format":"quack-v4-case-jsonl/v1","source_id":"source","guild_id":"guild",` +
	`"target_discord_user_id":"member","reason":"history","action_type":"warning",` +
	`"created_at":"2024-01-02T03:04:05Z"}`

// fakeRepository returns canned decisions and records audited failures.
type fakeRepository struct {
	preview, applied []Decision
	failureCodes     []string
}

func (f *fakeRepository) PreviewV4Import(context.Context, Batch, []PreparedCase) ([]Decision, error) {
	return f.preview, nil
}

func (f *fakeRepository) ApplyV4Import(context.Context, Batch, []PreparedCase) ([]Decision, error) {
	return f.applied, nil
}

func (f *fakeRepository) RollbackV4Import(context.Context, string, string, string) error { return nil }

func (f *fakeRepository) RecordV4ImportFailure(_ context.Context, _ Batch, _ int, code string) error {
	f.failureCodes = append(f.failureCodes, code)
	return nil
}

// importString imports input into guild "guild" through repository.
func importString(repository *fakeRepository, input string, dryRun bool) (*Report, error) {
	return New(repository).Import(context.Background(), "export", "guild", "actor", strings.NewReader(input), dryRun)
}

func TestImportRejectsMalformedRows(t *testing.T) {
	tests := map[string]string{
		"bad json":         "{bad json}\n",
		"second object":    validRow + validRow + "\n",
		"trailing garbage": validRow + "garbage\n",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			repository := &fakeRepository{}
			report, err := importString(repository, input, false)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Import = %v, want ErrInvalidInput", err)
			}
			if report == nil || report.Valid != 0 || len(report.Failures) != 1 || report.Failures[0].Code != "malformed_json" {
				t.Fatalf("got report %+v, want one malformed_json failure", report)
			}
			if want := []string{"validation_failed"}; !slices.Equal(repository.failureCodes, want) {
				t.Errorf("audited failures %q, want %q", repository.failureCodes, want)
			}
		})
	}
}

func TestImportReportsEveryBadRow(t *testing.T) {
	fixture, err := os.ReadFile("testdata/malformed_cases.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	report, err := importString(&fakeRepository{}, string(fixture), true)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Import = %v, want ErrInvalidInput", err)
	}
	want := []Issue{{Line: 1, Code: "malformed_json"}, {Line: 2, Code: "unsupported_action_type"}}
	if !slices.Equal(report.Failures, want) {
		t.Errorf("got failures %+v, want %+v", report.Failures, want)
	}
}

func TestImportFailedDryRunRecordsNothing(t *testing.T) {
	for name, input := range map[string]string{"malformed": "{bad json}\n", "empty": "\n"} {
		t.Run(name, func(t *testing.T) {
			repository := &fakeRepository{}
			if _, err := importString(repository, input, true); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Import = %v, want ErrInvalidInput", err)
			}
			if len(repository.failureCodes) != 0 {
				t.Errorf("dry run audited failures %q", repository.failureCodes)
			}
		})
	}
}

func TestImportDryRunReturnsPreview(t *testing.T) {
	repository := &fakeRepository{preview: []Decision{
		{Line: 1, SourceID: "source", WouldCreate: true, Warnings: []string{"target_departed"}},
	}}
	report, err := importString(repository, validRow, true)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if !report.DryRun || len(report.Decisions) != 1 || report.Decisions[0].Created {
		t.Fatalf("got report %+v, want the preview decision", report)
	}
	if len(report.Warnings) != 1 || report.Warnings[0] != (Issue{Line: 1, Code: "target_departed"}) {
		t.Errorf("got warnings %+v, want target_departed on line 1", report.Warnings)
	}
}

func TestImportReportsOversizedRowByLine(t *testing.T) {
	input := validRow + "\n\n" + strings.Repeat("x", maxRowBytes+1) + "\n"
	report, err := importString(&fakeRepository{}, input, true)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Import = %v, want ErrInvalidInput", err)
	}
	want := []Issue{{Line: 3, Code: "row_too_large"}}
	if !slices.Equal(report.Failures, want) {
		t.Errorf("got failures %+v, want %+v", report.Failures, want)
	}
}

func TestValidate(t *testing.T) {
	valid := LegacyCase{
		Format:              FormatVersion,
		SourceID:            "source",
		GuildID:             "guild",
		TargetDiscordUserID: "member",
		Reason:              "history",
		ActionType:          "ban",
		CreatedAt:           time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	tests := []struct {
		name   string
		change func(*LegacyCase)
		want   string
	}{
		{"valid", func(*LegacyCase) {}, ""},
		{"format", func(c *LegacyCase) { c.Format = "v0" }, "unsupported_format"},
		{"source", func(c *LegacyCase) { c.SourceID = " " }, "missing_source_id"},
		{"long source", func(c *LegacyCase) { c.SourceID = strings.Repeat("s", 192) }, "source_id_too_long"},
		{"guild", func(c *LegacyCase) { c.GuildID = "other" }, "guild_mismatch"},
		{"target", func(c *LegacyCase) { c.TargetDiscordUserID = "" }, "missing_target"},
		{"long moderator", func(c *LegacyCase) { c.ModeratorDiscordUserID = strings.Repeat("1", 33) }, "discord_identity_too_long"},
		{"reason", func(c *LegacyCase) { c.Reason = "" }, "missing_reason"},
		{"action", func(c *LegacyCase) { c.ActionType = "mute" }, "unsupported_action_type"},
		{"created at", func(c *LegacyCase) { c.CreatedAt = time.Time{} }, "missing_created_at"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := valid
			test.change(&row)
			if got := validate(row, "guild"); got != test.want {
				t.Errorf("validate = %q, want %q", got, test.want)
			}
		})
	}
}

func TestValidateCommandScopes(t *testing.T) {
	tests := []struct {
		name           string
		v4, v5         []string
		afterMigration bool
		wantErr        bool
	}{
		{"collision", []string{"Case"}, []string{"case"}, false, true},
		{"direct command after migration", []string{"warn"}, []string{"case"}, true, true},
		{"direct command before migration", []string{"warn"}, []string{"case"}, false, false},
		{"isolated", []string{"ticket"}, []string{"case"}, true, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateCommandScopes(test.v4, test.v5, test.afterMigration)
			if (err != nil) != test.wantErr {
				t.Errorf("ValidateCommandScopes = %v, want error %v", err, test.wantErr)
			}
		})
	}
}

func FuzzImport(f *testing.F) {
	f.Add(validRow)
	f.Add(`{bad json}`)
	f.Add("")
	f.Fuzz(func(t *testing.T, input string) {
		report, err := importString(&fakeRepository{}, input, true)
		if err == nil && (report == nil || report.Total < 1 || report.Valid < 1) {
			t.Fatalf("successful import returned report %+v", report)
		}
		if report == nil {
			return
		}
		for _, issue := range append(report.Warnings, report.Failures...) {
			if issue.Code == "" || issue.Line < 1 {
				t.Fatalf("issue %+v has no code or line", issue)
			}
		}
	})
}

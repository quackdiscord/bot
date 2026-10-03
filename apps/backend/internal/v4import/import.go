// Package v4import brings case history from Quack v4 into v5.
//
// Operators export v4 cases as versioned JSONL, one case per line, and import
// the file into one v5 guild with `quack import-v4 import`. Imported cases are
// history only: they show in a member's record but never count toward
// escalation. A file is validated whole before anything is written, so a bad
// row stops the whole import, and re-importing the same file is a no-op.
// Reports and audit entries name rows by line number and error code only;
// they never echo member data.
package v4import

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// FormatVersion is the format every row must declare. A future export format
// gets a new version so old importers reject it instead of misreading it.
const FormatVersion = "quack-v4-case-jsonl/v1"

// maxImportBytes caps a source file. The whole file is held in memory so it
// can be checksummed and validated before any write.
const maxImportBytes = 64 << 20

// maxRowBytes caps one JSONL row.
const maxRowBytes = 2 << 20

var (
	// ErrInvalidInput means the arguments or the source file cannot be
	// imported. Nothing was written.
	ErrInvalidInput = errors.New("invalid v4 import input")
	// ErrSourceCollision means a row reuses the source ID of an already
	// imported case but with different content.
	ErrSourceCollision = errors.New("v4 import source collision")
)

// LegacyCase is one row of a v4 export.
type LegacyCase struct {
	Format                 string     `json:"format"`
	SourceID               string     `json:"source_id"`
	CaseNumber             uint64     `json:"case_number"`
	GuildID                string     `json:"guild_id"`
	TargetDiscordUserID    string     `json:"target_discord_user_id"`
	ModeratorDiscordUserID string     `json:"moderator_discord_user_id,omitempty"`
	ModeratorDisplayName   string     `json:"moderator_display_name,omitempty"`
	Reason                 string     `json:"reason"`
	ActionType             string     `json:"action_type"`
	ContextURL             string     `json:"context_url,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	TargetDeparted         bool       `json:"target_departed,omitempty"`
	TargetMissing          bool       `json:"target_missing,omitempty"`
	ActionExpiresAt        *time.Time `json:"action_expires_at,omitempty"`
}

// PreparedCase is a validated row ready for the repository.
type PreparedCase struct {
	// Line is the row's 1-based line number in the source.
	Line int
	// Fingerprint is a hash of the row's content. A source ID that comes back
	// with a different fingerprint is a collision, not a re-import.
	Fingerprint string
	Case        LegacyCase
}

// Batch identifies one import of one source file. It carries no member data,
// so it is safe to store in the batch ledger and audit log.
type Batch struct {
	// ID is derived from the guild, source name, and checksum, so importing
	// the same file again yields the same batch.
	ID                 string
	GuildID            string
	SourceName         string
	Checksum           string
	ActorDiscordUserID string
	RecordCount        int
}

// Decision is what the repository did, or in a dry run would do, with one
// row.
type Decision struct {
	Line             int
	SourceCaseNumber uint64
	SourceID         string
	TargetCaseID     string
	// TargetCaseNumber is the v4 case number when it is free in the guild,
	// and the next free number otherwise.
	TargetCaseNumber uint64
	WouldCreate      bool
	Created          bool
	AlreadyImported  bool
	// Warnings are codes for things an operator should review, such as a
	// remapped case number or a member who has left.
	Warnings []string
}

// Report is the outcome of an import, printed for the operator. It is
// returned even when the import fails, to say which rows were rejected.
type Report struct {
	BatchID         string
	Checksum        string
	DryRun          bool
	Total           int
	Valid           int
	Created         int
	AlreadyImported int
	Warnings        []Issue
	Failures        []Issue
	Decisions       []Decision
}

// Issue flags one row by line number and code, never by content.
type Issue struct {
	Line int    `json:"line"`
	Code string `json:"code"`
}

// Repository stores imports. Applying a batch is atomic, and the repository
// owns case numbering so imported numbers never collide with v5 cases.
type Repository interface {
	PreviewV4Import(ctx context.Context, batch Batch, rows []PreparedCase) ([]Decision, error)
	ApplyV4Import(ctx context.Context, batch Batch, rows []PreparedCase) ([]Decision, error)
	RollbackV4Import(ctx context.Context, guildID, batchID, actorID string) error
	// RecordV4ImportFailure audits a failed import by count and code.
	RecordV4ImportFailure(ctx context.Context, batch Batch, failures int, code string) error
}

// Importer validates v4 exports and hands them to a Repository.
type Importer struct {
	repository Repository
}

// New returns an Importer that stores through repository.
func New(repository Repository) *Importer {
	return &Importer{repository: repository}
}

// Import reads a whole v4 export from input and imports it into guildID as
// actorID. sourceName is a stable name for the export, used with its checksum
// to recognize a re-import. With dryRun set it only reports what it would do.
//
// Any invalid row rejects the whole source. Failed real imports are audited.
func (i *Importer) Import(ctx context.Context, sourceName, guildID, actorID string, input io.Reader, dryRun bool) (*Report, error) {
	sourceName = strings.TrimSpace(sourceName)
	guildID = strings.TrimSpace(guildID)
	actorID = strings.TrimSpace(actorID)
	if sourceName == "" || guildID == "" || actorID == "" {
		return nil, fmt.Errorf("%w: source, guild, and actor are required", ErrInvalidInput)
	}
	if len(sourceName) > 191 || len(guildID) > 26 || len(actorID) > 32 {
		return nil, fmt.Errorf("%w: source, guild, or actor is too long", ErrInvalidInput)
	}
	raw, err := io.ReadAll(io.LimitReader(input, maxImportBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read v4 import: %w", err)
	}
	if len(raw) > maxImportBytes {
		return nil, fmt.Errorf("%w: source exceeds 64 MiB", ErrInvalidInput)
	}

	sum := sha256.Sum256(raw)
	checksum := hex.EncodeToString(sum[:])
	identity := sha256.Sum256([]byte(guildID + "\n" + sourceName + "\n" + checksum))
	rows, issues := parse(raw, guildID)
	batch := Batch{
		ID:                 "v4-" + hex.EncodeToString(identity[:12]),
		GuildID:            guildID,
		SourceName:         sourceName,
		Checksum:           checksum,
		ActorDiscordUserID: actorID,
		RecordCount:        len(rows) + len(issues),
	}
	report := &Report{
		BatchID:  batch.ID,
		Checksum: checksum,
		DryRun:   dryRun,
		Total:    batch.RecordCount,
		Valid:    len(rows),
		Failures: issues,
	}
	// Auditing is best effort: the import's own error matters more.
	recordFailure := func(count int, code string) {
		if !dryRun {
			_ = i.repository.RecordV4ImportFailure(ctx, batch, count, code)
		}
	}
	if len(issues) != 0 {
		recordFailure(len(issues), "validation_failed")
		return report, fmt.Errorf("%w: %d row(s) failed validation", ErrInvalidInput, len(issues))
	}
	if len(rows) == 0 {
		recordFailure(1, "empty_source")
		return report, fmt.Errorf("%w: source contains no records", ErrInvalidInput)
	}

	var decisions []Decision
	if dryRun {
		decisions, err = i.repository.PreviewV4Import(ctx, batch, rows)
	} else {
		decisions, err = i.repository.ApplyV4Import(ctx, batch, rows)
	}
	if err != nil {
		code := "storage_failed"
		if errors.Is(err, ErrSourceCollision) {
			code = "source_collision"
		}
		recordFailure(1, code)
		return report, err
	}
	report.Decisions = decisions
	for _, decision := range decisions {
		if decision.Created {
			report.Created++
		}
		if decision.AlreadyImported {
			report.AlreadyImported++
		}
		for _, code := range decision.Warnings {
			report.Warnings = append(report.Warnings, Issue{Line: decision.Line, Code: code})
		}
	}
	return report, nil
}

// Rollback removes the cases one batch imported. The repository refuses when
// any of them has since gained v5 state, such as actions or appeals.
func (i *Importer) Rollback(ctx context.Context, guildID, batchID, actorID string) error {
	return i.repository.RollbackV4Import(ctx,
		strings.TrimSpace(guildID), strings.TrimSpace(batchID), strings.TrimSpace(actorID))
}

// ValidateCommandScopes checks that the v4 and v5 bots can run side by side:
// no command name is registered by both. With afterMigration set it also
// requires that v4's direct moderation commands are gone, since v5 cases
// replace them.
func ValidateCommandScopes(v4Names, v5Names []string, afterMigration bool) error {
	v5 := make(map[string]bool, len(v5Names))
	for _, name := range v5Names {
		v5[strings.ToLower(strings.TrimSpace(name))] = true
	}
	for _, name := range v4Names {
		name = strings.ToLower(strings.TrimSpace(name))
		if v5[name] {
			return fmt.Errorf("command scope collision: %s", name)
		}
		if afterMigration && (name == "warn" || name == "timeout" || name == "kick" || name == "ban") {
			return fmt.Errorf("legacy direct moderation command remains after migration: %s", name)
		}
	}
	return nil
}

// parse splits raw into rows, skipping blank lines, and returns the valid
// rows and an issue for each invalid one.
func parse(raw []byte, guildID string) ([]PreparedCase, []Issue) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64*1024), maxRowBytes)
	var rows []PreparedCase
	var issues []Issue
	line := 1
	for ; scanner.Scan(); line++ {
		body := bytes.TrimSpace(scanner.Bytes())
		if len(body) == 0 {
			continue
		}
		row, err := decodeRow(body)
		if err != nil {
			issues = append(issues, Issue{Line: line, Code: "malformed_json"})
			continue
		}
		if code := validate(row, guildID); code != "" {
			issues = append(issues, Issue{Line: line, Code: code})
			continue
		}
		canonical, _ := json.Marshal(row)
		digest := sha256.Sum256(canonical)
		rows = append(rows, PreparedCase{Line: line, Fingerprint: hex.EncodeToString(digest[:]), Case: row})
	}
	if scanner.Err() != nil {
		// The scanner stops at an oversized row, so line is that row.
		issues = append(issues, Issue{Line: line, Code: "row_too_large"})
	}
	return rows, issues
}

// decodeRow decodes exactly one JSON object with no unknown fields and
// nothing after it.
func decodeRow(body []byte) (LegacyCase, error) {
	var row LegacyCase
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&row); err != nil {
		return LegacyCase{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return LegacyCase{}, errors.New("trailing data after row")
	}
	return row, nil
}

// validate returns the code for the first problem with row, or "" when it can
// be imported into guildID. Length limits match the v5 columns.
func validate(row LegacyCase, guildID string) string {
	switch {
	case row.Format != FormatVersion:
		return "unsupported_format"
	case strings.TrimSpace(row.SourceID) == "":
		return "missing_source_id"
	case len(row.SourceID) > 191:
		return "source_id_too_long"
	case strings.TrimSpace(row.GuildID) != guildID:
		return "guild_mismatch"
	case strings.TrimSpace(row.TargetDiscordUserID) == "":
		return "missing_target"
	case len(row.TargetDiscordUserID) > 32 || len(row.ModeratorDiscordUserID) > 32:
		return "discord_identity_too_long"
	case strings.TrimSpace(row.Reason) == "":
		return "missing_reason"
	case !isLegacyAction(row.ActionType):
		return "unsupported_action_type"
	case row.CreatedAt.IsZero():
		return "missing_created_at"
	}
	return ""
}

// isLegacyAction reports whether action is a v4 action type.
func isLegacyAction(action string) bool {
	switch action {
	case "warning", "timeout", "kick", "ban":
		return true
	}
	return false
}

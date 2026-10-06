package quack_test

import (
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
)

func TestIsStarterTemplate(t *testing.T) {
	archived := time.Now()
	tests := []struct {
		name   string
		modify func(*quack.ExpandedCaseTemplate)
		want   bool
	}{
		{name: "unchanged", modify: func(*quack.ExpandedCaseTemplate) {}, want: true},
		{name: "renamed", modify: func(t *quack.ExpandedCaseTemplate) { t.Template.Name = "Spam" }},
		{name: "not appealable", modify: func(t *quack.ExpandedCaseTemplate) { t.Template.Appealable = false }},
		{name: "archived", modify: func(t *quack.ExpandedCaseTemplate) { t.Template.ArchivedAt = &archived }},
		{name: "level removed", modify: func(t *quack.ExpandedCaseTemplate) { t.Levels = t.Levels[:2] }},
		{name: "threshold changed", modify: func(t *quack.ExpandedCaseTemplate) { t.Levels[1].Level.TriggerCaseCount = 4 }},
		{name: "action changed", modify: func(t *quack.ExpandedCaseTemplate) { t.Levels[2].Actions[0].ActionType = quack.ActionKickUser }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template := quack.StarterTemplate()
			tt.modify(&template)
			if got := quack.IsStarterTemplate(template); got != tt.want {
				t.Fatalf("IsStarterTemplate() = %v, want %v", got, tt.want)
			}
		})
	}
}

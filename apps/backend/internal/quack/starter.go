package quack

// The starter template's identity. Bootstrap matches an existing template by
// slug and then checks its policy with IsStarterTemplate.
const (
	starterSlug = "general-rule-violation"
	starterName = "General rule violation"
)

// StarterTemplate returns the template Quack creates when it joins a guild:
// a general rule with a warning by default, a 24-hour timeout at three
// cases, and a ban at five. Admins are prompted to review it and can edit it
// like any other template.
func StarterTemplate() ExpandedCaseTemplate {
	return ExpandedCaseTemplate{
		Template: CaseTemplate{
			Slug:                   starterSlug,
			Name:                   starterName,
			Description:            "A starter rule for general violations. Review and customize it for this guild.",
			ReasonTemplate:         starterName,
			Appealable:             true,
			Version:                1,
			CreatedByDiscordUserID: systemActorID,
			UpdatedByDiscordUserID: systemActorID,
		},
		Levels: []ExpandedCaseTemplateLevel{
			{
				Level: CaseTemplateLevel{Position: 1, Name: "Default", IsDefault: true, NotifyUser: true},
			},
			{
				Level: CaseTemplateLevel{Position: 2, Name: "24-hour timeout", TriggerCaseCount: 3, NotifyUser: true},
				Actions: []CaseTemplateLevelAction{
					{ActionType: ActionTimeoutUser, ConfigJSON: `{"duration_seconds":86400}`},
				},
			},
			{
				Level: CaseTemplateLevel{Position: 3, Name: "Ban", TriggerCaseCount: 5, NotifyUser: true},
				Actions: []CaseTemplateLevelAction{
					{ActionType: ActionBanUser, ConfigJSON: `{"delete_message_seconds":86400}`},
				},
			},
		},
	}
}

// IsStarterTemplate reports whether t still has the starter template's
// policy. Bootstrap uses it to avoid adopting an unrelated template that
// happens to use the starter slug.
func IsStarterTemplate(t ExpandedCaseTemplate) bool {
	want := StarterTemplate()
	if t.Template.Name != want.Template.Name ||
		t.Template.ReasonTemplate != want.Template.ReasonTemplate ||
		!t.Template.Appealable ||
		t.Template.ArchivedAt != nil ||
		len(t.Levels) != len(want.Levels) {
		return false
	}
	for i, wantLevel := range want.Levels {
		got := t.Levels[i]
		if got.Level.IsDefault != wantLevel.Level.IsDefault ||
			got.Level.TriggerCaseCount != wantLevel.Level.TriggerCaseCount ||
			!got.Level.NotifyUser ||
			len(got.Actions) != len(wantLevel.Actions) {
			return false
		}
		if len(wantLevel.Actions) == 1 &&
			(got.Actions[0].ActionType != wantLevel.Actions[0].ActionType ||
				got.Actions[0].ConfigJSON != wantLevel.Actions[0].ConfigJSON) {
			return false
		}
	}
	return true
}

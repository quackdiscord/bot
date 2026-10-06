package discord

import "github.com/quackdiscord/bot/internal/quack"

// Hooks for the appeal and template tests in package discord_test.

// Appeals exposes the appeal handlers.
type Appeals struct{ a appeals }

func NewAppeals(services *quack.Services) Appeals { return Appeals{appeals{services: services}} }

func (a Appeals) OpenForm() Handler                  { return a.a.openForm }
func (a Appeals) Submit() Handler                    { return a.a.submit }
func (a Appeals) Decide(action string) Handler       { return a.a.decide(action) }
func (a Appeals) DecideReason(action string) Handler { return a.a.decideWithReason(action) }
func (a Appeals) StatementPage(delta int) Handler    { return a.a.statementPage(delta) }
func (a Appeals) Command() Handler                   { return a.a.command }
func (a Appeals) QueuePage() Handler                 { return a.a.queuePage }

// Templates exposes the /template handlers.
type Templates struct{ t templates }

func NewTemplates(services *quack.Services) Templates {
	return Templates{templates{services: services}}
}

func (t Templates) Command() Handler      { return t.t.command }
func (t Templates) CreateSubmit() Handler { return t.t.createSubmit }

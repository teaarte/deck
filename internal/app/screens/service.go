package screens

import (
	"context"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Service draws deck's screens and the menu: it reads as the viewer through the registry and counts through the
// functions of the other services.
type Service struct {
	St       *store.Store
	Registry *registry.Registry
	// Waiting, Open and Failing count by project what waits for the owner, runs and fails; Failed and Unread are the
	// failed runs and the notes not read.
	Waiting func(ctx context.Context, act registry.Actor) (map[string]int64, error)
	Open    func(ctx context.Context, act registry.Actor) (map[string]int64, error)
	Failing func(ctx context.Context, act registry.Actor) (map[string]int64, error)
	Failed  func(ctx context.Context, act registry.Actor) (int64, error)
	Unread  func(ctx context.Context, act registry.Actor) (int64, error)
	// Works looks up the model that does the work of a command.
	Works func(ctx context.Context, workspace int64, lang string) func(projectID int64, command string) provider.Work
	// ScreenFailed and ScreenDrawn tell how a screen of a project drew, ScreenDrawn with the blocks that drew when it
	// read not all of them; File is a file a media block shows.
	ScreenFailed func(ctx context.Context, p db.Project, revision int64, name string, errs []string)
	ScreenDrawn  func(ctx context.Context, p db.Project, name string, good []string)
	File         func(ctx context.Context, p db.Project, ref any) (map[string]any, error)
	// Language is the language of the interface the workspace picked.
	Language func(ctx context.Context, workspace int64) string
	// Theme is the name of the theme the workspace wears.
	Theme func(ctx context.Context, workspace int64) string
}

// Commands are the commands of screens and the menu.
func (ss *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Def("menu", "Menu",
			"Lists the screens to navigate: deck's own, then each project's menu.",
			registry.OwnerSide, ss.menu),
		registry.Def("menu_order", "Order the menu",
			"Sets the order of the projects of the side list of the menu, the same on every device.",
			registry.OwnerSide, ss.menuOrderSet),
		registry.Def("screen", "Screen",
			"Resolves a stored screen with its data into A2UI messages; with a topic, only the data of the blocks it changes, with a block only that of the block; the blocks of a dialog get data only while it is open. Anyone but the owner gets the text of a document and the HTML of a custom block by size, and whole by a read of that block.",
			registry.OwnerSide, ss.screenGet),
		registry.Read("commands_list", "Commands",
			"Lists the commands the caller may call, deck's and those of projects, with their file fields and what runs them; to the master also those only the owner calls, with the address of their form. command_describe gives a description.",
			registry.OwnerSide, ss.commandsList).On("projects", "tools", "assignments", "models", "triggers", "health", "documents"),
		registry.Read("command_describe", "Describe command",
			"Gives the parameters of any command of the workspace, also of one only the owner calls, such as the forms of Settings: its input and result schemas, class and callers.",
			registry.OwnerSide, ss.commandDescribe).On(registry.Static),
	}
}

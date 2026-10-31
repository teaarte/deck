package harness

type MenuView struct {
	Project string
	Icon    string
	Screens []struct{ Name, Title, Icon string }
	Open    *struct{ Name string }
	Running int64
	Asks    int64
	Errors  int64
}

// SideMenu is the projects of the side list of deck.menu by name, in their order, and the master's project.
func (e *Env) SideMenu() ([]string, string) {
	e.T.Helper()
	var menu struct {
		Projects []struct{ Project string }
		Master   *struct{ Project string }
	}
	e.Must("deck.menu", map[string]any{}, &menu)
	names := []string{}
	for _, p := range menu.Projects {
		names = append(names, p.Project)
	}
	if menu.Master == nil {
		return names, ""
	}
	return names, menu.Master.Project
}

package harness

type AgentsV struct {
	Agents []struct{ Name, Status string }
	Models []struct {
		Name, Status string
		Builtin      bool
	}
}

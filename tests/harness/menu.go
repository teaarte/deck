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

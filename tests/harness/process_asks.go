package harness

type PressAsk struct {
	ID    int64          `json:"id"`
	Kind  string         `json:"kind"`
	What  string         `json:"what"`
	Where string         `json:"where"`
	Allow map[string]any `json:"allow"`
	Deny  map[string]any `json:"deny"`
}

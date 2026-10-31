package harness

// ChatD5 is what the chat block gives beside the topic: suggestions, decisions of the turn and how replies ended.
type ChatD5 struct {
	Suggest []struct{ Icon, Text string } `json:"suggest"`
	Asks    []struct {
		ID      int64
		Kind    string
		What    string
		Actions []struct {
			Verb, Label, Command, Until string
			Primary, Hold               bool
			Params                      map[string]any
		} `json:"actions"`
	} `json:"asks"`
	Messages []struct {
		ID    int64
		Role  string
		Reply *struct {
			Cut *struct {
				Stopped bool
				Reason  string
				Steps   int
				Said    string
				Retry   []struct {
					Verb, Command string
					Params        map[string]any
				}
			}
		}
	} `json:"messages"`
}

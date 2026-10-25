package provider

// Work is who does a part of the work of a project: the kind of work, the model it goes to now, the models assigned
// to it in order, the models of deck that could take it, and the price of the model it goes to.
type Work struct {
	Role    string   `json:"role,omitempty" enum:"agent|text|image|tts|stt|music|video|search"`
	Model   string   `json:"model,omitempty"`
	Models  []string `json:"models,omitempty"`
	Choices []string `json:"choices,omitempty"`
	Price   string   `json:"price,omitempty"`
	// Why is one line of the health of the first model that is down, when the work goes to another.
	Why string `json:"why,omitempty"`
}

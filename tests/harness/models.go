package harness

type ModelsOut struct {
	Agents []struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Status  string `json:"status"`
		Error   string `json:"error"`
	} `json:"agents"`
	Connections []ModelsOutConnection `json:"connections"`
	Models      []struct {
		Builtin    bool     `json:"builtin"`
		Name       string   `json:"name"`
		Connection string   `json:"connection"`
		Model      string   `json:"model"`
		Kind       string   `json:"kind"`
		Agent      string   `json:"agent"`
		InputUSD   *float64 `json:"input_usd"`
		OutputUSD  *float64 `json:"output_usd"`
		CallUSD    *float64 `json:"call_usd"`
		Free       bool     `json:"free"`
	} `json:"models"`
	Assignments []AssignmentOut `json:"assignments"`
}

type AssignmentOut struct {
	Project string   `json:"project"`
	Command string   `json:"command"`
	Role    string   `json:"role"`
	Models  []string `json:"models"`
	Using   string   `json:"using"`
	Why     string   `json:"why"`
}

type ModelsOutConnection struct {
	Name   string `json:"name"`
	API    string `json:"api"`
	URL    string `json:"url"`
	Key    string `json:"key"`
	Local  bool   `json:"local"`
	Models int    `json:"models"`
}

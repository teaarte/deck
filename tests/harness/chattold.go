package harness

type ToldMessage struct {
	Role    string `json:"role"`
	Text    string `json:"text"`
	Process int64  `json:"process"`
	Told    *struct {
		ID      int64  `json:"id"`
		Project string `json:"project"`
		Command string `json:"command"`
		State   string `json:"state"`
		Word    string `json:"word"`
		Tone    string `json:"tone"`
		Reason  string `json:"reason"`
		Error   string `json:"error"`
		Open    struct {
			Screen string `json:"screen"`
			Record int64  `json:"record"`
		} `json:"open"`
	} `json:"told"`
}

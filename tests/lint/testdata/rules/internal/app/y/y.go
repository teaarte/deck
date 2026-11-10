package y

type event struct{ Topic string }

func f(c interface{ On(...string) }, topic string) {
	c.On("projects", "log")
	_ = event{Topic: "log"}
	_ = topic == "projects"
	_ = []string{"projects", "log"}
	_ = map[string]string{"log": "projects"}
	_ = "projects"
}

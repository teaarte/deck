package tools

import (
	"net/http"
	"os"
	"os/exec"
)

var c = &http.Client{}
var _ = os.WriteFile("x", nil, 0o600)
var _ = os.Getenv("HOME")
var _ = exec.Command

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
var _, _ = http.Get("http://x")
var _, _ = exec.LookPath("claude")
var _, _ = os.ReadFile("x")
var _, _ = os.Open("x")
var _, _ = os.OpenFile("x", os.O_RDONLY, 0)
var _, _ = os.OpenFile("x", os.O_CREATE|os.O_WRONLY, 0o600)

package fault

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestStatus(t *testing.T) {
	refusal := New(CallNotFound, "deck: no such record")
	for _, row := range []struct {
		err  error
		want int
		what string
	}{
		{New(CallRefused, "deck: no"), http.StatusForbidden, "a refusal of the caller"},
		{New(CallInvalid, "deck: bad"), http.StatusUnprocessableEntity, "a refusal of the input"},
		{refusal, http.StatusNotFound, "a refusal of what is not there"},
		{New(CallConflict, "deck: changed"), http.StatusConflict, "a call on a state that changed"},
		{New(CallGone, "deck: answered"), http.StatusGone, "a call on what is over"},
		{New(CallUnsigned, "deck: who"), http.StatusUnauthorized, "a call from no one deck knows"},
		{New(CallLimited, "deck: later"), http.StatusTooManyRequests, "an address past its tries"},
		{New(CallUnkeyed, "deck: touch"), http.StatusPreconditionRequired, "a call that wants a fresh passkey"},
		{fmt.Errorf("reading: %w", refusal), http.StatusNotFound, "a refusal wrapped keeps its status"},
		{Wrap("deck was doing", refusal), http.StatusInternalServerError, "a refusal under deck's own failure is deck's"},
		{New(ModelLimit, "limit"), http.StatusTooManyRequests, "a model's limit"},
		{New(ModelUnavailable, "down"), http.StatusBadGateway, "a model that did not answer"},
		{New(ModelLogin, "login"), http.StatusUnauthorized, "a model that wants a login"},
		{New(Network, "net"), http.StatusBadGateway, "the network"},
		{New(RightsDenied, "denied"), http.StatusForbidden, "a right refused"},
		{New(InputInvalid, "bad"), http.StatusUnprocessableEntity, "an input a script refused"},
		{New(ScriptFailed, "failed"), http.StatusInternalServerError, "a failure of another type"},
		{errors.New("boom"), http.StatusInternalServerError, "an error of no type"},
	} {
		if got := Status(row.err); got != row.want {
			t.Errorf("%s: Status = %d, want %d", row.what, got, row.want)
		}
	}
}

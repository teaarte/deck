package auth

import (
	"net/http"
	"strings"
)

// Resolve is the one reading of what a request presents to say whom it comes from: the token after Bearer in the
// header Authorization, else the value of the cookie named cookie; bearer says it came in the header.
func Resolve(r *http.Request, cookie string) (raw string, bearer bool) {
	if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && token != "" {
		return token, true
	}
	if cookie == "" {
		return "", false
	}
	if c, err := r.Cookie(cookie); err == nil {
		return c.Value, false
	}
	return "", false
}

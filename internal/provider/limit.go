package provider

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// LimitEnds is when a usage limit a model API answered with ends, in unix seconds, by its resets or by its words: a
// minute's or a day's (to 00:00 UTC); zero when the answer tells nothing.
func LimitEnds(h http.Header, words string, now time.Time) int64 {
	type answer struct {
		Error struct {
			Metadata struct {
				Headers map[string]any `json:"headers"`
			} `json:"metadata"`
			Details []struct {
				RetryDelay string `json:"retryDelay"`
			} `json:"details"`
		} `json:"error"`
	}
	// OpenRouter puts X-RateLimit-* in the metadata of its error; Gemini a retryDelay in its details, in a list on
	// its OpenAI-compatible API.
	var body answer
	if json.Unmarshal([]byte(words), &body) != nil {
		var list []answer
		if json.Unmarshal([]byte(words), &list) == nil && len(list) > 0 {
			body = list[0]
		}
	}
	inner := http.Header{}
	for k, v := range body.Error.Metadata.Headers {
		switch v := v.(type) {
		case string:
			inner.Set(k, v)
		case float64:
			inner.Set(k, strconv.FormatFloat(v, 'f', -1, 64))
		}
	}
	var end time.Time
	for _, h := range []http.Header{h, inner} {
		if t := retryAfter(h.Get("Retry-After"), now); t.After(end) {
			end = t
		}
		for _, of := range []string{"", "-Requests", "-Tokens"} {
			if left := h.Get("X-Ratelimit-Remaining" + of); left != "" && left != "0" {
				continue
			}
			if t := resetAt(h.Get("X-Ratelimit-Reset"+of), now); t.After(end) {
				end = t
			}
		}
	}
	for _, d := range body.Error.Details {
		if t := resetAt(d.RetryDelay, now); t.After(end) {
			end = t
		}
	}
	text := strings.ToLower(words)
	says := func(w string) bool { return strings.Contains(text, w) }
	switch {
	case slices.ContainsFunc([]string{"per-day", "per_day", "per day", "perday", "rpd", "tpd", "daily"}, says):
		// A day's limit lasts the day, though Gemini's retryDelay says seconds.
		y, m, d := now.UTC().Date()
		return max(end.Unix(), time.Date(y, m, d+1, 0, 0, 0, 0, time.UTC).Unix())
	case end.After(now):
		return end.Unix()
	case slices.ContainsFunc([]string{"per-min", "per_min", "per min", "perminute", "rpm", "tpm"}, says):
		return now.Add(time.Minute).Unix()
	}
	return 0
}

// retryAfter reads Retry-After: seconds or an HTTP date.
func retryAfter(v string, now time.Time) time.Time {
	if s, err := strconv.Atoi(v); err == nil && s > 0 {
		return now.Add(time.Duration(s) * time.Second)
	}
	if t, err := http.ParseTime(v); err == nil {
		return t
	}
	return time.Time{}
}

// resetAt reads a reset as APIs give it: unix milliseconds or seconds, seconds from now, or a duration as "2m59.5s".
func resetAt(v string, now time.Time) time.Time {
	v = strings.TrimSpace(v)
	if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 {
		switch {
		case n > 1e12:
			return time.UnixMilli(int64(n))
		case n > 1e9:
			return time.Unix(int64(n), 0)
		}
		return now.Add(time.Duration(n * float64(time.Second)))
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return now.Add(d)
	}
	return time.Time{}
}

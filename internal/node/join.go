package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"deck2/internal/egress"
	"deck2/internal/home"
)

// Address is deck's address as the owner gave it to a node: http or https, a host, no path.
func Address(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Hostname() == "" || u.Path != "" || u.RawQuery != "" || u.User != nil {
		return "", fmt.Errorf("%q is not deck's address, such as https://deck.example.com or http://127.0.0.1:7700", raw)
	}
	if u.Scheme == "http" && !egress.IsLoopback(u.Hostname()) {
		return "", fmt.Errorf("%s is plain http to another machine, where the key of the node would cross the network in clear: join deck at its https address", raw)
	}
	return raw, nil
}

// Pair pairs this machine with deck at address by a code of deck pair --node and gives the key deck answered.
func Pair(ctx context.Context, address, code, name string) (Joined, error) {
	body, _ := json.Marshal(map[string]string{"code": strings.TrimSpace(code), "name": name})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, address+"/auth/pair/begin", bytes.NewReader(body))
	if err != nil {
		return Joined{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", address)
	resp, err := egress.ToDeck.RoundTrip(req)
	if err != nil {
		return Joined{}, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var out struct {
		Key    string `json:"key"`
		Device int64  `json:"device"`
		Error  string `json:"error"`
	}
	_ = json.Unmarshal(b, &out)
	if resp.StatusCode != http.StatusOK || out.Key == "" {
		return Joined{}, fmt.Errorf("deck did not pair this node (%s): %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return Joined{Deck: address, Key: out.Key, Device: out.Device}, nil
}

// Save keeps what a node joined in its home, readable by its user alone.
func Save(h home.Layout, j Joined) error {
	b, _ := json.Marshal(j)
	return home.WriteFile(h.NodeKey(), b)
}

// ErrNotJoined is a node whose home keeps no key of deck.
var ErrNotJoined = errors.New("this machine is not paired with deck: run deck -join <deck's address> <code>, the code of deck pair --node on deck's machine")

// Load is what the node of a home joined.
func Load(h home.Layout) (Joined, error) {
	b, err := os.ReadFile(h.NodeKey())
	if errors.Is(err, os.ErrNotExist) {
		return Joined{}, ErrNotJoined
	}
	if err != nil {
		return Joined{}, err
	}
	var j Joined
	if err := json.Unmarshal(b, &j); err != nil || j.Key == "" {
		return Joined{}, ErrNotJoined
	}
	return j, nil
}

package main

import (
	"cmp"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"deck2/internal/app"
	"deck2/internal/auth"
	deckhome "deck2/internal/home"
)

// pair is deck pair: it writes a code that pairs one device where deck reads it, running or not, and prints the
// links that carry it; the launcher takes its link from here.
func pair(args []string) error {
	set := flag.NewFlagSet("deck pair", flag.ContinueOnError)
	device := set.String("device", auth.Browser, "what pairs: browser, launcher or node")
	name := set.String("name", "", "the name of the device in the list of devices")
	if err := set.Parse(args); err != nil {
		return err
	}
	h, err := homeDir()
	if err != nil {
		return err
	}
	code, until, err := app.PairCode(h, *device, *name)
	if err != nil {
		return err
	}
	origins := auth.ReadOrigins(h.Origins())
	switch *device {
	case auth.Launcher:
		fmt.Println(local(origins) + "/#pair=" + code)
		return nil
	case auth.Node:
		fmt.Println(code)
		return nil
	}
	links := auth.Links(origins, code)
	if len(links) == 0 {
		return errors.New("a browser pairs with deck only over https, and deck has no https address yet: start it with -tailscale or -public, then run deck pair again")
	}
	fmt.Printf("Open one of these links on the device by %s, once:\n", until.Format("15:04"))
	for _, l := range links {
		fmt.Println(l)
	}
	return nil
}

// local is deck's address on this machine the launcher's window opens, from the origins deck kept or its -addr.
func local(origins []auth.Origin) string {
	for _, o := range origins {
		if strings.HasPrefix(o.Main, "http://localhost:") {
			return o.Main
		}
	}
	_, port, err := net.SplitHostPort(cmp.Or(os.Getenv("DECK_ADDR"), "127.0.0.1:7700"))
	if err != nil {
		port = "7700"
	}
	return "http://localhost:" + port
}

// homeDir is deck's home: DECK_HOME, else .deck2 in the user's home.
func homeDir() (deckhome.Layout, error) {
	if h := os.Getenv("DECK_HOME"); h != "" {
		return deckhome.Layout(h), nil
	}
	u, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return deckhome.Layout(filepath.Join(u, ".deck2")), nil
}

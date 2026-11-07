package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"deck2/internal/app"
	"deck2/internal/auth"
	deckhome "deck2/internal/home"
)

// pair is deck pair: it writes a code that pairs one device where deck reads it, running or not, and prints the
// links that carry it.
func pair(args []string) error {
	set := flag.NewFlagSet("deck pair", flag.ContinueOnError)
	device := set.String("device", auth.Browser, "what pairs: browser or node")
	node := set.Bool("node", false, "pair a node, the same as -device node")
	name := set.String("name", "", "the name of the device in the list of devices")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *node {
		*device = auth.Node
	}
	h, err := homeDir()
	if err != nil {
		return err
	}
	if err := asOwner(h); err != nil {
		return err
	}
	code, until, err := app.PairCode(h, *device, *name)
	if err != nil {
		return err
	}
	origins := auth.ReadOrigins(h.Origins())
	if *device == auth.Node {
		joins := auth.Joins(origins, code)
		if len(joins) == 0 {
			joins = []string{"deck -join <deck's https address> " + code + " --install"}
		}
		fmt.Printf("On the node's machine run one of these by %s, once:\n", until.Format("15:04"))
		for _, j := range joins {
			fmt.Println(j)
		}
		return nil
	}
	links := auth.Links(origins, code)
	if len(links) == 0 {
		return errors.New("a browser pairs with deck only over https or at deck.localhost on its machine, and deck has neither: start it once on loopback, or with -tailscale or -public, then run deck pair again")
	}
	fmt.Printf("Open one of these links on the device by %s, once:\n", until.Format("15:04"))
	for _, l := range links {
		fmt.Println(l)
	}
	return nil
}

// asOwner makes deck pair, run by root as docker compose exec runs it, write its code as the user whose deck's home
// is, so deck reads it.
func asOwner(h deckhome.Layout) error {
	fi, err := os.Stat(string(h))
	if err != nil || os.Geteuid() != 0 {
		return nil
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Uid == 0 {
		return nil
	}
	if err := syscall.Setgid(int(st.Gid)); err != nil {
		return err
	}
	return syscall.Setuid(int(st.Uid))
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

package main

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// words are the launcher's own words by language; the window is deck itself, in deck's language.
var words = map[string]map[string]string{
	"ru": {
		"Open deck": "Открыть deck", "Start at login": "Запускать при входе", "Quit": "Выйти",
		"deck did not start": "deck не запустился", "Starting deck…": "deck запускается…",
		"Installing WSL; Windows asks for consent and may need a restart.": "Ставлю WSL; Windows спросит разрешение и может попросить перезагрузку.",
		"Installing Ubuntu for deck as the distribution deck.": "Ставлю Ubuntu для deck — дистрибутив deck.",
		"Preparing the distribution: bubblewrap and the user deck.": "Готовлю дистрибутив: bubblewrap и пользователь deck.",
		"Putting deck into the distribution.": "Кладу deck в дистрибутив.",
		"restart Windows, then open deck again": "перезагрузите Windows и откройте deck снова",
		"deck is ready in WSL.": "deck готов в WSL.",
	},
}

// lang is the language of the system's interface, en when deck has no words for it.
var lang = func() string {
	l := os.Getenv("LANG")
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("defaults", "read", "-g", "AppleLanguages").Output(); err == nil {
			if f := strings.Fields(strings.Trim(string(out), "() \n")); len(f) > 0 {
				l = strings.Trim(f[0], `",`)
			}
		}
	}
	if runtime.GOOS == "windows" {
		l = windowsLanguage()
	}
	if len(l) >= 2 && words[l[:2]] != nil {
		return l[:2]
	}
	return "en"
}()

// tr is a word of the launcher in its language.
func tr(s string) string {
	if w, ok := words[lang][s]; ok {
		return w
	}
	return s
}

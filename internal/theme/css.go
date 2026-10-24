package theme

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/tdewolff/parse/v2"
	"github.com/tdewolff/parse/v2/css"
)

// Root is the page in a selector: one :root is not enough to win over Mantine's own variables.
const Root = ":root:root:root"

var roots = map[string]string{"root": Root, "light": Root + "[data-mantine-color-scheme='light']", "dark": Root + "[data-mantine-color-scheme='dark']"}

var (
	attrs        = []string{"data-variant", "data-active", "data-disabled", "data-checked", "data-expanded", "data-cover", "data-big"}
	pseudos      = []string{"hover", "active", "focus", "focus-visible", "focus-within", "disabled", "first-child", "last-child"}
	elements     = []string{"before", "after", "placeholder"}
	functions    = []string{"rgb", "rgba", "hsl", "hsla", "hwb", "lab", "lch", "oklab", "oklch", "color-mix", "linear-gradient", "radial-gradient", "conic-gradient", "repeating-linear-gradient", "repeating-radial-gradient", "calc", "min", "max", "clamp", "var", "blur", "saturate", "brightness", "contrast", "grayscale", "opacity", "drop-shadow", "hue-rotate", "translate", "translatex", "translatey", "scale", "rotate", "cubic-bezier", "steps"}
	mediaFeature = regexp.MustCompile(`^(prefers-[a-z-]+|forced-colors|hover|any-hover|pointer|any-pointer|min-width|max-width)$`)
	property     = regexp.MustCompile(`^-?[a-z][a-z-]*$`)
	attrValue    = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	banned       = []string{"behavior", "-moz-binding", "-ms-behavior"}
)

// variable is the CSS variable a theme name of one stands for: a token, the accent, a palette color, a radius or the
// theme's own.
func variable(name string) (string, error) {
	n := strings.TrimPrefix(name, "--")
	if t, ok := Catalog.Tokens[n]; ok {
		return t.Var, nil
	}
	if v, ok := Catalog.Vars[n]; ok {
		return v, nil
	}
	if strings.HasPrefix(n, "theme-") && property.MatchString(n) {
		return "--" + n, nil
	}
	if rest, ok := strings.CutPrefix(n, "accent-"); ok && len(rest) == 1 && rest[0] >= '0' && rest[0] <= '9' {
		return "--mantine-primary-color-" + rest, nil
	}
	if rest, ok := strings.CutPrefix(n, "color-"); ok {
		if i := strings.LastIndexByte(rest, '-'); i > 0 {
			if _, ok := Palette.Colors[rest[:i]]; ok {
				if k, err := strconv.Atoi(rest[i+1:]); err == nil && k >= 0 && k <= 9 {
					return "--mantine-color-" + rest, nil
				}
			}
		}
	}
	return "", fmt.Errorf("no variable %s: a theme reads its tokens, --accent, --accent-<0..9>, --color-<name>-<0..9>, the radii, the spacings and --theme-<name>", name)
}

func lex(v string) []css.Token {
	l := css.NewLexer(parse.NewInputString(v))
	var out []css.Token
	for {
		tt, data := l.Next()
		if tt == css.ErrorToken {
			return out
		}
		out = append(out, css.Token{TokenType: tt, Data: slices.Clone(data)})
	}
}

// Value checks a CSS value and writes it with the variables of deck.
func Value(v string) (string, error) {
	if strings.Contains(v, `\`) {
		return "", errors.New("no backslashes in a theme")
	}
	return value(lex(v))
}

func value(ts []css.Token) (string, error) {
	var b strings.Builder
	var last css.Token
	for i := 0; i < len(ts); i++ {
		t := ts[i]
		if t.TokenType == css.LeftParenthesisToken && last.TokenType == css.IdentToken {
			return "", fmt.Errorf("%s ( is no function: a function is written %s(…)", last.Data, last.Data)
		}
		if t.TokenType != css.WhitespaceToken && t.TokenType != css.CommentToken {
			last = t
		}
		switch t.TokenType {
		case css.URLToken, css.BadURLToken:
			return "", errors.New("no url() in a theme")
		case css.BadStringToken, css.AtKeywordToken, css.LeftBracketToken, css.RightBracketToken, css.LeftBraceToken,
			css.RightBraceToken, css.SemicolonToken, css.CDOToken, css.CDCToken:
			return "", fmt.Errorf("%q has no place in a value", t.Data)
		case css.CommentToken, css.WhitespaceToken:
			b.WriteByte(' ')
			continue
		case css.FunctionToken:
			name := strings.ToLower(strings.TrimSuffix(string(t.Data), "("))
			if !slices.Contains(functions, name) {
				return "", fmt.Errorf("no %s() in a theme", name)
			}
			if name == "var" {
				j := i + 1
				for j < len(ts) && ts[j].TokenType == css.WhitespaceToken {
					j++
				}
				if j == len(ts) || ts[j].TokenType != css.CustomPropertyNameToken && ts[j].TokenType != css.IdentToken {
					return "", errors.New("var() takes a variable")
				}
				v, err := variable(string(ts[j].Data))
				if err != nil {
					return "", err
				}
				b.WriteString("var(" + v)
				i = j
				continue
			}
		case css.CustomPropertyNameToken:
			return "", errors.New("a variable is read with var()")
		case css.IdentToken:
			if strings.HasPrefix(string(t.Data), "--") {
				return "", errors.New("a variable is read with var()")
			}
		}
		b.Write(t.Data)
	}
	return strings.TrimSpace(b.String()), nil
}

// selector writes a selector of the classes of the catalog as the selector of the page it stands for; page says one of
// its selectors is a root class alone, the page itself.
func selector(ts []css.Token) (sel string, page bool, err error) {
	var out []string
	var cur strings.Builder
	start, class, alone := true, false, false
	for i := 0; i < len(ts); i++ {
		t := ts[i]
		switch t.TokenType {
		case css.CommaToken:
			if !class {
				return "", false, errors.New("a selector names a class of the catalog")
			}
			out = append(out, finish(cur.String()))
			cur.Reset()
			page = page || alone
			start, class, alone = true, false, false
		case css.WhitespaceToken:
			if class {
				cur.WriteByte(' ')
			}
		case css.DelimToken:
			switch d := string(t.Data); d {
			case ".":
				if i+1 == len(ts) || ts[i+1].TokenType != css.IdentToken {
					return "", false, errors.New("a class has a name")
				}
				i++
				name := string(ts[i].Data)
				if r, ok := roots[name]; ok {
					if !start || i+1 < len(ts) && ts[i+1].TokenType != css.WhitespaceToken && ts[i+1].TokenType != css.CommaToken {
						return "", false, fmt.Errorf(".%s only starts a selector", name)
					}
					cur.WriteString(r)
					start, class, alone = false, true, true
					continue
				}
				sel, ok := Catalog.Classes[name]
				if !ok {
					return "", false, fmt.Errorf("no class .%s in the catalog", name)
				}
				if !start && !strings.HasSuffix(cur.String(), " ") {
					return "", false, fmt.Errorf(".%s starts a step: join classes with a space or >", name)
				}
				if start {
					cur.WriteString(Root + " ")
				}
				cur.WriteString(sel)
				start, class, alone = false, true, false
			case ">", "+", "~":
				if !class {
					return "", false, fmt.Errorf("%s comes between classes", d)
				}
				cur.WriteString(" " + d + " ")
			default:
				return "", false, fmt.Errorf("%q has no place in a selector", d)
			}
		case css.LeftBracketToken, css.ColonToken, css.FunctionToken:
			if !class || strings.HasSuffix(cur.String(), " ") {
				return "", false, errors.New("a state follows a class")
			}
			n, s, err := state(ts[i:])
			if err != nil {
				return "", false, err
			}
			cur.WriteString(s)
			alone = false
			i += n - 1
		default:
			return "", false, fmt.Errorf("%q has no place in a selector: name classes of the catalog", t.Data)
		}
	}
	if !class {
		return "", false, errors.New("a selector names a class of the catalog")
	}
	return strings.Join(append(out, finish(cur.String())), ", "), page || alone, nil
}

func finish(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, Root+" ") || strings.HasPrefix(s, Root) {
		return s
	}
	return Root + " " + s
}

// state is one state at the start of ts: an attribute, a pseudo-class, :not() of states or a pseudo-element; n is how
// many tokens it takes.
func state(ts []css.Token) (int, string, error) {
	switch ts[0].TokenType {
	case css.LeftBracketToken:
		end := slices.IndexFunc(ts, func(t css.Token) bool { return t.TokenType == css.RightBracketToken })
		if end < 2 || ts[1].TokenType != css.IdentToken || !slices.Contains(attrs, string(ts[1].Data)) {
			return 0, "", fmt.Errorf("a theme reads the states %s", strings.Join(attrs, ", "))
		}
		s := "[" + string(ts[1].Data)
		switch end {
		case 2:
		case 4:
			v := strings.Trim(string(ts[3].Data), `"'`)
			if string(ts[2].Data) != "=" || ts[3].TokenType != css.IdentToken && ts[3].TokenType != css.StringToken || !attrValue.MatchString(v) {
				return 0, "", errors.New("an attribute is [name] or [name=value] of letters, digits, - and _")
			}
			s += "='" + v + "'"
		default:
			return 0, "", errors.New("an attribute is [name] or [name=value]")
		}
		return end + 1, s + "]", nil
	case css.ColonToken:
		if len(ts) > 2 && ts[1].TokenType == css.ColonToken && ts[2].TokenType == css.IdentToken && slices.Contains(elements, string(ts[2].Data)) {
			return 3, "::" + string(ts[2].Data), nil
		}
		if len(ts) > 1 && ts[1].TokenType == css.IdentToken && slices.Contains(pseudos, string(ts[1].Data)) {
			return 2, ":" + string(ts[1].Data), nil
		}
		if len(ts) > 1 && ts[1].TokenType == css.FunctionToken && string(ts[1].Data) == "not(" {
			var b strings.Builder
			for i := 2; i < len(ts); {
				if ts[i].TokenType == css.RightParenthesisToken {
					return i + 1, ":not(" + b.String() + ")", nil
				}
				if ts[i].TokenType != css.LeftBracketToken && ts[i].TokenType != css.ColonToken {
					return 0, "", errors.New(":not() takes states")
				}
				n, s, err := state(ts[i:])
				if err != nil {
					return 0, "", err
				}
				b.WriteString(s)
				i += n
			}
		}
		return 0, "", fmt.Errorf("a theme reads :%s, ::%s and :not()", strings.Join(pseudos, ", :"), strings.Join(elements, ", ::"))
	}
	return 0, "", errors.New("a state is an attribute or a pseudo-class")
}

func prelude(name string, ts []css.Token) (string, error) {
	switch name {
	case "@media":
		for i, t := range ts {
			if t.TokenType == css.LeftParenthesisToken && i+1 < len(ts) && ts[i+1].TokenType == css.IdentToken && !mediaFeature.MatchString(string(ts[i+1].Data)) {
				return "", fmt.Errorf("@media of %s: a theme reads prefers-*, forced-colors, hover, pointer, min-width and max-width", ts[i+1].Data)
			}
		}
	case "@supports":
	default:
		return "", fmt.Errorf("no %s in a theme: @media and @supports only", name)
	}
	var b strings.Builder
	for _, t := range ts {
		switch t.TokenType {
		case css.URLToken, css.BadURLToken, css.StringToken, css.BadStringToken, css.LeftBraceToken, css.SemicolonToken:
			return "", fmt.Errorf("%q has no place in %s", t.Data, name)
		case css.FunctionToken:
			if f := strings.ToLower(strings.TrimSuffix(string(t.Data), "(")); !slices.Contains(functions, f) {
				return "", fmt.Errorf("no %s() in a theme", f)
			}
		}
		b.Write(t.Data)
	}
	return name + " " + strings.TrimSpace(b.String()), nil
}

// Compile checks the CSS of a theme and writes it for the page; what it refuses it leaves out and names.
func Compile(src string) (string, []string) {
	if strings.Contains(src, `\`) {
		return "", []string{"css: no backslashes in a theme"}
	}
	var out strings.Builder
	var problems []string
	bad := func(where string, err error) { problems = append(problems, fmt.Sprintf("css: %s: %v", where, err)) }
	p := css.NewParser(parse.NewInputString(src), false)
	skip, open := 0, 0
	sel, root := "", false
	for {
		g, _, data := p.Next()
		if skip > 0 {
			switch g {
			case css.BeginAtRuleGrammar, css.BeginRulesetGrammar:
				skip++
			case css.EndAtRuleGrammar, css.EndRulesetGrammar:
				skip--
			case css.ErrorGrammar:
				if !errors.Is(p.Err(), io.EOF) {
					bad("parse", p.Err())
				}
				return out.String(), problems
			}
			continue
		}
		switch g {
		case css.ErrorGrammar:
			if !errors.Is(p.Err(), io.EOF) {
				bad("parse", p.Err())
			}
			if open > 0 {
				problems = append(problems, "css: a block is not closed")
				return "", problems
			}
			return out.String(), problems
		case css.CommentGrammar:
		case css.AtRuleGrammar:
			bad(string(data), fmt.Errorf("no %s in a theme: @media and @supports only", data))
		case css.BeginAtRuleGrammar:
			pre, err := prelude(strings.ToLower(string(data)), p.Values())
			if err != nil {
				bad(string(data), err)
				skip = 1
				continue
			}
			out.WriteString(pre + " {\n")
			open++
		case css.EndAtRuleGrammar, css.EndRulesetGrammar:
			out.WriteString("}\n")
			open--
		case css.BeginRulesetGrammar:
			s, page, err := selector(p.Values())
			if err != nil {
				bad(text(p.Values()), err)
				skip = 1
				continue
			}
			sel = text(p.Values())
			root = page
			out.WriteString(s + " {\n")
			open++
		case css.DeclarationGrammar, css.CustomPropertyGrammar:
			name := strings.ToLower(string(data))
			var ts []css.Token
			if g == css.CustomPropertyGrammar {
				v, err := variable(name)
				if err != nil {
					bad(sel, err)
					continue
				}
				if _, ok := Catalog.Tokens[strings.TrimPrefix(name, "--")]; ok && root {
					bad(sel, fmt.Errorf("%s of the page is a token: set it in tokens, light or dark, where its contrast is checked", name))
					continue
				}
				name = v
				for _, t := range p.Values() {
					ts = append(ts, lex(string(t.Data))...)
				}
			} else {
				if !property.MatchString(name) || slices.Contains(banned, name) {
					bad(sel, fmt.Errorf("no property %s in a theme", name))
					continue
				}
				ts = p.Values()
			}
			v, err := value(ts)
			if err != nil {
				bad(sel+" "+name, err)
				continue
			}
			out.WriteString("  " + name + ": " + v + ";\n")
		default:
			bad(string(data), errors.New("stray text"))
		}
	}
}

func text(ts []css.Token) string {
	var b strings.Builder
	for _, t := range ts {
		b.Write(t.Data)
	}
	return b.String()
}

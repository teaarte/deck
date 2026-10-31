package schemas

import (
	"encoding/json"
	"regexp"
	"strings"
)

// The names deck gives things, one rule a kind; they differ where the kinds differ.
var (
	// Name names the parts of a manifest and the folders of a project: $defs/name of the manifest schema.
	Name = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,39}$`)
	// ProjectName names a project, a connection and a model; no _, as it stands in tool names.
	ProjectName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	// SecretName names a secret: as an API names its keys, with capitals and dots.
	SecretName = regexp.MustCompile(`^` + SecretChar + `{1,64}$`)
	// ThemeName names a theme, a file name of its pictures; it may begin with a digit.
	ThemeName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
)

// SecretChar is a character of the name of a secret.
const SecretChar = `[A-Za-z0-9_.-]`

// ValidSecret says a name may name a secret: SecretName, and not only dots, which name folders.
func ValidSecret(name string) bool {
	return SecretName.MatchString(name) && strings.Trim(name, ".") != ""
}

func init() {
	var m struct {
		Defs struct{ Name struct{ Pattern string } } `json:"$defs"`
	}
	if json.Unmarshal(Manifest, &m) != nil || m.Defs.Name.Pattern != Name.String() {
		panic("schemas: $defs/name of manifest.json is not Name")
	}
}

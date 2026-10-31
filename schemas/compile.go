package schemas

import (
	"bytes"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Compile compiles a JSON Schema that stands at url; a fragment of url compiles that part of it.
func Compile(raw []byte, url string) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	at, _, _ := strings.Cut(url, "#")
	if err := c.AddResource(at, doc); err != nil {
		return nil, err
	}
	return c.Compile(url)
}

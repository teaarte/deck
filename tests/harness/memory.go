package harness

import (
	"fmt"
)

func Fact(name, typ, body string) string {
	return fmt.Sprintf("---\nname: %s\ndescription: %s\ntype: %s\n---\n\n%s\n", name, body, typ, body)
}

package manifest

import (
	"maps"
	"slices"
	"strings"
)

// source checks what a block reads and returns the fields of its items; nil fields is any name.
func (c *screenCheck) source(where, typ string, src Source) ([]string, bool) {
	from := src.From
	if from == "" {
		from = "collection"
	}
	if (typ == "log") != (from == LogSource) {
		c.fail("%s: log blocks and only they read %s", where, LogSource)
		return nil, false
	}
	if from == "collection" && (src.Input != nil || src.Rows != "") {
		c.fail("%s source: a collection takes no input or rows", where)
		return nil, false
	}
	var fields []string
	switch {
	case from == "collection" && src.Project != "":
		if err := c.m.checkForeignSource(src); err != nil {
			c.fail("%s source: %v", where, err)
			return nil, false
		}
		return nil, true
	case from == "collection":
		col := c.m.collections[src.Collection]
		if col == nil {
			c.fail("%s: unknown collection %q; collections are %s", where, src.Collection, strings.Join(slices.Sorted(maps.Keys(c.m.collections)), ", "))
			return nil, false
		}
		if col.fields != nil {
			fields = append(slices.Clone(col.fields), "_id", "_updated")
		}
	case src.Collection != "" || src.Project != "":
		c.fail("%s source: %s takes no collection or project", where, from)
		return nil, false
	case from == LogSource && c.screen.Record != DeckRecord:
		c.fail("%s source: the log is read on a page of a process", where)
		return nil, false
	case from == LogSource:
	default:
		rc, ok := Reads(from)
		if !ok {
			// A saved module keeps loading when deck drops a read: the block is an error, a save is refused.
			report := c.warn
			if c.deck {
				report = c.fail
			}
			report("%s source: %s is not a deck command that only reads", where, from)
			return nil, false
		}
		if fields, ok = rc.Fields(src.Rows); !ok {
			c.fail("%s source: %s gives no list of objects (rows %q)", where, from, src.Rows)
			return nil, false
		}
		if fields != nil {
			fields = append(slices.Clone(fields), "_id")
		}
	}
	if fields != nil && c.screen.Record != "" {
		fields = append(fields, "record")
	}
	fields = c.withParam(fields)
	if src.Where != "" {
		if _, err := compileField(src.Where, fields); err != nil {
			c.fail("%s where: %v", where, err)
		}
	}
	if f := strings.TrimPrefix(src.Sort, "-"); f != "" && fields != nil && !slices.Contains(fields, f) {
		c.fail("%s sort: unknown field %q; the fields are %s", where, f, strings.Join(slices.Sorted(slices.Values(fields)), ", "))
	}
	return fields, true
}

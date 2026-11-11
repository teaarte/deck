package home

import "testing"

func TestWithin(t *testing.T) {
	for _, row := range []struct {
		dir, path string
		within    bool
		what      string
	}{
		{"/a", "/a", true, "the folder itself"},
		{"/a", "/a/b", true, "a file in it"},
		{"/a", "/a/b/c", true, "a file deeper"},
		{"/a/", "/a/b", true, "a folder written with a slash"},
		{"/a", "/ab", false, "a sibling that starts the same"},
		{"/a", "/a/../b", false, "a way out by dots"},
		{"/a", "/a/b/../../a/c", true, "dots that come back in"},
		{"/a", "/a/..b", true, "a name that starts with two dots"},
		{"/a", "/a/../a", true, "the folder by a way round"},
		{"/a", "/", false, "the root above it"},
		{"/a/b", "/a", false, "the parent"},
		{"/", "/x", true, "under the root"},
		{"/a", "b", false, "a relative path against an absolute folder"},
		{"a", "a/b", true, "two relative paths"},
		{"a", "../a/b", false, "a relative path that leaves first"},
	} {
		if got := Within(row.dir, row.path); got != row.within {
			t.Errorf("Within(%q, %q) = %v, want %v: %s", row.dir, row.path, got, row.within, row.what)
		}
	}
}

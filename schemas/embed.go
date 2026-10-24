package schemas

import _ "embed"

//go:embed manifest.json
var Manifest []byte

// Catalog is the A2UI catalog of screen blocks; the web client reads the same file.
//
//go:embed catalog.json
var Catalog []byte

// Theme is the schema of a theme of deck with the catalog of classes and tokens a theme reads.
//
//go:embed theme.json
var Theme []byte

// Palette is the colors of deck; the web client reads the same file.
//
//go:embed palette.json
var Palette []byte

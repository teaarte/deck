package schemas

import _ "embed"

//go:embed manifest.json
var Manifest []byte

// Catalog is the A2UI catalog of screen blocks; the web client reads the same file.
//
//go:embed catalog.json
var Catalog []byte

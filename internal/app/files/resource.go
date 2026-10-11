package files

import (
	"context"
	"encoding/base64"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"deck2/internal/app/access"
	"deck2/internal/registry"
)

// resourceLimit is the biggest file a custom block reads.
const resourceLimit = 50 << 20

// reading lets two files at most be held for custom blocks at once; the rest wait.
var reading = make(chan struct{}, 2)

type fileReadIn struct {
	Project string `type:"project" desc:"The project of the screen of the custom block"`
	URI     string `desc:"deck://file/<id> for a stored file of the project, deck://data/<path> for a file of its data folder"`
}

// resource is a file of a project whole, as the contents of an MCP resources/read.
type resource struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size"`
	Blob string `json:"blob" desc:"the file in base64"`
}

// fileRead gives a custom block a file of the project of its screen: a stored file of that project or a file
// inside its data folder, never one of another project or outside the folder.
func (s *Service) fileRead(ctx context.Context, act registry.Actor, in fileReadIn) (resource, error) {
	p, err := access.Project(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return resource{}, err
	}
	if err := access.Files(act, p.Name, false); err != nil {
		return resource{}, err
	}
	u, err := url.Parse(in.URI)
	rest := ""
	if err == nil {
		rest = strings.TrimPrefix(u.Path, "/")
	}
	if err != nil || u.Scheme != "deck" || u.Opaque != "" || u.User != nil || !filepath.IsLocal(rest) {
		return resource{}, registry.Invalid("deck: a custom block reads deck://file/<id> or deck://data/<path> of its module, not %q", in.URI)
	}
	out := resource{URI: in.URI}
	var f *os.File
	switch u.Host {
	case "file":
		id, err := strconv.ParseInt(rest, 10, 64)
		if err != nil {
			return resource{}, registry.Invalid("deck: a stored file is deck://file/<id>, not %q", in.URI)
		}
		row, err := s.St.Q.GetFile(ctx, id)
		if err != nil || row.ProjectID != p.ID {
			return resource{}, registry.NotFound("deck: no file %d in %s", id, p.Name)
		}
		if f, err = os.Open(filepath.Join(s.Home.Files(), strconv.FormatInt(row.ID, 10))); err != nil {
			return resource{}, registry.NotFound("deck: no file %d in %s", id, p.Name)
		}
		out.Name, out.Type = row.Name, row.Type
	case "data":
		if f, err = s.dataFile(p.ID, rest); err != nil {
			return resource{}, registry.NotFound("deck: no file %q in the data folder of %s", rest, p.Name)
		}
		out.Name, out.Type = filepath.Base(rest), fileType(rest)
	default:
		return resource{}, registry.Invalid("deck: a custom block reads deck://file/<id> or deck://data/<path> of its module, not %q", in.URI)
	}
	defer f.Close()
	select {
	case reading <- struct{}{}:
		defer func() { <-reading }()
	case <-ctx.Done():
		return resource{}, ctx.Err()
	}
	fi, err := f.Stat()
	if err != nil {
		return resource{}, err
	}
	if out.Size = fi.Size(); out.Size > resourceLimit {
		return resource{}, registry.Invalid("deck: %s is %d MB; a custom block reads files up to 50 MB", out.Name, out.Size>>20)
	}
	b, err := io.ReadAll(io.LimitReader(f, resourceLimit+1))
	if err != nil {
		return resource{}, err
	}
	if len(b) > resourceLimit {
		return resource{}, registry.Invalid("deck: %s grew past 50 MB; a custom block reads files up to 50 MB", out.Name)
	}
	out.Size, out.Blob = int64(len(b)), base64.StdEncoding.EncodeToString(b)
	return out, nil
}

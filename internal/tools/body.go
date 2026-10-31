package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"slices"
	"strings"

	"deck2/internal/files"
	"deck2/internal/manifest"
	"deck2/internal/nonce"
	"deck2/internal/registry"
)

// body is what an HTTP tool sends: JSON, one file as it is, or multipart form parts of which some are
// files. Files are opened anew for every request.
type body struct {
	json  []byte
	file  *files.Ref
	parts []part
}

type part struct {
	name string
	text string
	file *files.Ref
}

// makeBody reads the body template of a tool: form parts take the input as text, a JSON body the values of json
// fields; resolve turns a file field into the file it points to.
func makeBody(t manifest.HTTPTool, in, values map[string]any, resolve func(string) (files.Ref, error)) (*body, error) {
	fileOf := func(tmpl string) (*files.Ref, bool, error) {
		m := field.FindStringSubmatch(tmpl)
		if m == nil || m[0] != tmpl || !slices.Contains(manifest.FieldsOf(t.Input, "file"), m[1]) {
			return nil, false, nil
		}
		ref, _ := in[m[1]].(string)
		if ref == "" {
			return nil, true, registry.Invalid("%s needs the file field %s", t.Name, m[1])
		}
		f, err := resolve(ref)
		return &f, true, err
	}
	var kinds map[string]json.RawMessage
	if json.Unmarshal(t.Body, &kinds) == nil {
		if raw, ok := kinds["multipart"]; ok {
			var tmpl map[string]string
			if err := json.Unmarshal(raw, &tmpl); err != nil {
				return nil, registry.Invalid("multipart body of %s: %v", t.Name, err)
			}
			b := &body{}
			for _, name := range slices.Sorted(maps.Keys(tmpl)) {
				f, isFile, err := fileOf(tmpl[name])
				if err != nil {
					return nil, err
				}
				if isFile {
					b.parts = append(b.parts, part{name: name, file: f})
					continue
				}
				b.parts = append(b.parts, part{name: name, text: field.ReplaceAllStringFunc(tmpl[name], func(m string) string {
					return fmt.Sprint(in[m[1:len(m)-1]])
				})})
			}
			return b, nil
		}
		if raw, ok := kinds["file"]; ok {
			var tmpl string
			_ = json.Unmarshal(raw, &tmpl)
			f, _, err := fileOf(tmpl)
			if err != nil {
				return nil, err
			}
			if f == nil {
				return nil, registry.Invalid("the file body of %s must be exactly one file field", t.Name)
			}
			return &body{file: f}, nil
		}
	}
	var tmpl any
	d := json.NewDecoder(bytes.NewReader(t.Body))
	d.UseNumber()
	if err := d.Decode(&tmpl); err != nil {
		return nil, registry.Invalid("body template of %s: %v", t.Name, err)
	}
	b, _ := json.Marshal(fill(tmpl, values))
	return &body{json: b}, nil
}

func size(f *files.Ref) (int64, error) {
	file, n, err := f.Open()
	if err != nil {
		return 0, fmt.Errorf("deck: %v", err)
	}
	file.Close()
	return n, nil
}

// attach gives a request the body; a file is opened anew for every request.
func (b *body) attach(r *http.Request) (*http.Request, error) {
	switch {
	case b == nil:
	case b.json != nil:
		r.Body, r.ContentLength = io.NopCloser(bytes.NewReader(b.json)), int64(len(b.json))
		r.Header.Set("Content-Type", "application/json")
	case b.file != nil:
		f, total, err := b.file.Open()
		if err != nil {
			return nil, err
		}
		r.Body, r.ContentLength = f, total
		r.Header.Set("Content-Type", b.file.Type)
	default:
		boundary := "deck-" + nonce.Hex(16)
		length, err := b.multipart(io.Discard, boundary, false)
		if err != nil {
			return nil, err
		}
		pr, pw := io.Pipe()
		go func() { _, err := b.multipart(pw, boundary, true); pw.CloseWithError(err) }()
		r.Body, r.ContentLength = pr, length
		r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	}
	return r, nil
}

// multipart writes the form to w and returns its length; without files it only counts their sizes. The boundary
// is random, so no file holds it, and the same in both passes.
func (b *body) multipart(w io.Writer, boundary string, withFiles bool) (int64, error) {
	cw := &counter{w: w}
	mw := multipart.NewWriter(cw)
	_ = mw.SetBoundary(boundary)
	for _, p := range b.parts {
		if p.file == nil {
			if err := mw.WriteField(p.name, p.text); err != nil {
				return 0, err
			}
			continue
		}
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, quoteForm(p.name), quoteForm(p.file.Name)))
		h.Set("Content-Type", p.file.Type)
		pw, err := mw.CreatePart(h)
		if err != nil {
			return 0, err
		}
		if !withFiles {
			n, err := size(p.file)
			if err != nil {
				return 0, err
			}
			cw.n += n
			continue
		}
		f, n, err := p.file.Open()
		if err != nil {
			return 0, err
		}
		_, err = io.Copy(pw, io.LimitReader(f, n))
		_ = f.Close()
		if err != nil {
			return 0, err
		}
	}
	err := mw.Close()
	return cw.n, err
}

var formQuote = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\r", "", "\n", "")

func quoteForm(s string) string { return formQuote.Replace(s) }

type counter struct {
	w io.Writer
	n int64
}

func (c *counter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

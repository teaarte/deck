package sandbox

import (
	"encoding/base64"
	"io"
	"os"
	"slices"
	"strings"

	"deck2/internal/untrusted"
)

// mask takes the secrets of a box out of what its program writes, a secret split across writes too.
type mask struct {
	secrets []string
	pend    []byte
}

// secretsOf are the secrets of b and of its door: the key, also as a proxy's Basic credentials.
func secretsOf(b Box, door *Door) []string {
	secrets := slices.Clone(b.Secrets)
	if door != nil {
		secrets = append(secrets, door.key, base64.StdEncoding.EncodeToString([]byte("deck:"+door.key)))
	}
	return secrets
}

// masking is a mask of one stream, nil without secrets.
func masking(secrets []string) *mask {
	if len(secrets) == 0 {
		return nil
	}
	return &mask{secrets: secrets}
}

// push is what of b and the text before it may go on masked; the end of it that may begin a secret waits for more,
// or goes as is when final.
func (m *mask) push(b []byte, final bool) []byte {
	s := untrusted.Redact(string(m.pend)+string(b), m.secrets)
	keep := 0
	if !final {
		keep = m.held(s)
	}
	m.pend = []byte(s[len(s)-keep:])
	return []byte(s[:len(s)-keep])
}

// held is the length of the longest end of s that begins a secret.
func (m *mask) held(s string) int {
	for k := min(len(s), m.longest()-1); k > 0; k-- {
		for _, v := range m.secrets {
			if strings.HasPrefix(v, s[len(s)-k:]) {
				return k
			}
		}
	}
	return 0
}

func (m *mask) longest() int {
	n := 0
	for _, v := range m.secrets {
		n = max(n, len(v))
	}
	return n
}

// maskedWriter writes to w what the program writes, masked.
type maskedWriter struct {
	w io.Writer
	m *mask
}

func (mw *maskedWriter) Write(b []byte) (int, error) {
	if out := mw.m.push(b, false); len(out) > 0 {
		if _, err := mw.w.Write(out); err != nil {
			return 0, err
		}
	}
	return len(b), nil
}

func (mw *maskedWriter) flush() {
	if out := mw.m.push(nil, true); len(out) > 0 {
		_, _ = mw.w.Write(out)
	}
	if f, ok := mw.w.(flusher); ok {
		f.flush()
	}
}

// flusher passes on what it holds once its program is done.
type flusher interface{ flush() }

// masked reads the output of a program masked, a chunk at a time.
type masked struct {
	f   *os.File
	m   *mask
	buf []byte
	out []byte
	err error
}

func (r *masked) Read(p []byte) (int, error) {
	for len(r.out) == 0 {
		if r.err != nil {
			return 0, r.err
		}
		if r.buf == nil {
			r.buf = make([]byte, 32<<10)
		}
		n, err := r.f.Read(r.buf)
		r.out, r.err = r.m.push(r.buf[:n], err != nil), err
	}
	n := copy(p, r.out)
	r.out = r.out[n:]
	return n, nil
}

func (r *masked) Close() error { return r.f.Close() }

// writerOf is w masked by m, w itself without a mask.
func writerOf(w io.Writer, m *mask) io.Writer {
	if m == nil {
		return w
	}
	return &maskedWriter{w: w, m: m}
}

// readerOf is f masked by m, f itself without a mask.
func readerOf(f *os.File, m *mask) io.ReadCloser {
	if m == nil {
		return f
	}
	return &masked{f: f, m: m}
}

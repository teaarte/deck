package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"deck2/internal/app"
	"deck2/internal/auth"
	"deck2/internal/fault"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/theme"
	"deck2/internal/words"
)

type Server struct {
	app *app.App
	web fs.FS
	// origins are the addresses deck answers the owner at, each with its second origin.
	origins *auth.Origins
	// plain says the owner came by an address deck does not sign in at since deck started.
	plain atomic.Bool
}

func New(a *app.App, web fs.FS) *Server { return &Server{app: a, web: web, origins: a.Auth.Origins()} }

// maxUpload is how much one upload of files may hold together.
const maxUpload = 1 << 30

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/mcp/", loopbackOnly(s.app.Gateway))
	mux.HandleFunc("/mcp", s.automation)
	mux.Handle("/", s.framesOnly(http.FileServerFS(s.web)))
	mux.HandleFunc("GET /custom", s.custom)
	s.rites(mux)

	api := http.NewServeMux()
	api.HandleFunc("POST /api/commands/{name}", s.invoke)
	api.HandleFunc("GET /api/events", s.ownerOnly(s.events))
	api.HandleFunc("POST /api/files", s.upload)
	api.HandleFunc("GET /api/files/{id}", s.download)
	api.HandleFunc("GET /api/files/data/{project}/{path...}", s.downloadData)
	api.HandleFunc("GET /api/files/theme/{name}/{file}", s.themePicture)
	mux.Handle("/api/", s.signed(api))
	return s.hosted(mux)
}

// loopbackOnly serves the processes of this machine, which come from loopback or from deck's own address;
// the tailnet and the network do not reach their tokens.
func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fromHere(r) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// fromHere says a request comes from this machine: from loopback or from deck's own address.
func fromHere(r *http.Request) bool {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	self := ""
	if la, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		self, _, _ = net.SplitHostPort(la.String())
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && (ip.Unmap().IsLoopback() || host == self)
}

type actorKey struct{}

// signed lets a call through as whom Who says it comes from.
func (s *Server) signed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		act, err := s.Who(r)
		if err != nil {
			status := fault.Status(err)
			if status == http.StatusUnauthorized {
				// The challenge tells a refused token of deck from a model's refused key a command answers with.
				w.Header().Set("WWW-Authenticate", "Bearer")
			}
			refuse(w, r, err.Error(), status)
			return
		}
		ctx := registry.WithLang(context.WithValue(r.Context(), actorKey{}, act), r.Header.Get("Deck-Lang"))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// automation is the MCP entry of automation tokens: Who says whose token it is.
func (s *Server) automation(w http.ResponseWriter, r *http.Request) {
	act, err := s.Who(r)
	if err == nil && act.Kind != registry.Token {
		err = errUnsigned
	}
	if err != nil {
		if fault.Status(err) == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", "Bearer")
		}
		refuse(w, r, err.Error(), fault.Status(err))
		return
	}
	s.app.Gateway.ServeAutomation(w, r, act)
}

func actor(r *http.Request) registry.Actor { return r.Context().Value(actorKey{}).(registry.Actor) }

func (s *Server) ownerOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if act := actor(r); act.Kind != registry.Owner && !act.Looks {
			refuse(w, r, "deck: automation tokens call commands only", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (s *Server) invoke(w http.ResponseWriter, r *http.Request) {
	body, err := registry.ReadBody(w, r)
	if err != nil {
		return
	}
	var out any
	if act := actor(r); act.Kind == registry.Token {
		out, err = s.app.Gateway.CallAs(r.Context(), act, r.PathValue("name"), body)
	} else {
		out, err = s.app.Registry.Invoke(r.Context(), act, r.PathValue("name"), body)
	}
	if err != nil {
		refuse(w, r, err.Error(), fault.Status(err))
		return
	}
	raw, err := json.Marshal(out)
	if err != nil {
		fail(w, err)
		return
	}
	// A read says the topics after which the client reads it again.
	if c := registry.ReadOf(r.PathValue("name")); c != nil {
		w.Header().Set("Deck-Topics", strings.Join(c.Topics, ","))
	}
	// Buttons are labeled in the language of the call, as the command reads it: its lang, else the client's.
	var in struct {
		Lang string `json:"lang"`
	}
	_ = json.Unmarshal(body, &in)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(manifest.Labeled(raw, words.In(cmp.Or(in.Lang, registry.Lang(r.Context())))))
}

// refuse answers a refusal of deck in the language the client reads, by its Deck-Lang header.
func refuse(w http.ResponseWriter, r *http.Request, text string, status int) {
	if lang := registry.Lang(r.Context()); lang != "" {
		text = manifest.TrText(words.In(lang), text)
	}
	http.Error(w, text, status)
}

// readsOnly refuses a write from a caller that only reads, such as a page deck opens itself.
const readsOnly = "deck: a page deck opens itself only reads"

// upload takes the files of a multipart form for a project; commands then get them by id.
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	if actor(r).ReadOnly {
		refuse(w, r, readsOnly, http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	mr, err := r.MultipartReader()
	if err != nil {
		refuse(w, r, "deck: send the files as multipart/form-data", http.StatusBadRequest)
		return
	}
	var out []any
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			refuse(w, r, err.Error(), http.StatusBadRequest)
			return
		}
		if part.FileName() == "" {
			continue
		}
		f, err := s.app.Files.Upload(r.Context(), actor(r), r.URL.Query().Get("project"), part.FileName(), part.Header.Get("Content-Type"), part)
		if err != nil {
			refuse(w, r, err.Error(), fault.Status(err))
			return
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		refuse(w, r, "deck: the form has no file", http.StatusBadRequest)
		return
	}
	send(w, out)
}

// download sends a stored file as an attachment, never as a page of deck's origin; a file of an id never changes, so
// the browser keeps it.
func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		refuse(w, r, "deck: a file id is a number", http.StatusBadRequest)
		return
	}
	f, file, err := s.app.Files.OpenFile(r.Context(), actor(r), id)
	if err == nil {
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	}
	serveFile(w, r, f.Name, f.Type, file, err)
}

// downloadData gives a file of the data folder of a project, which a media block shows.
func (s *Server) downloadData(w http.ResponseWriter, r *http.Request) {
	open := s.app.Files.OpenDataFile
	if r.URL.Query().Get("thumb") != "" {
		open = s.app.Files.OpenDataThumb
	}
	f, file, err := open(r.Context(), actor(r), r.PathValue("project"), r.PathValue("path"))
	serveFile(w, r, f.Name, f.Type, file, err)
}

func serveFile(w http.ResponseWriter, r *http.Request, name, typ string, file *os.File, err error) {
	if err != nil {
		refuse(w, r, err.Error(), fault.Status(err))
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", typ)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	http.ServeContent(w, r, "", time.Time{}, file)
}

// alive says the sign-in of a stream still lasts: a lent token until it is back, the session of a device until it ends
// or the device is revoked, checked after the stream is held so a revoke meanwhile is not missed.
func (s *Server) alive(r *http.Request) func(context.Context) bool {
	raw, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if c, err := r.Cookie(cookieName); err == nil && raw == "" {
		raw = c.Value
	}
	if _, ok := s.app.Owners.Lent(raw); ok {
		return func(context.Context) bool { _, ok := s.app.Owners.Lent(raw); return ok }
	}
	if actor(r).Device == 0 {
		return func(context.Context) bool { return true }
	}
	return func(ctx context.Context) bool { return s.app.Auth.Alive(ctx, raw) }
}

// pingEvery is how often the live channel says it is there, so a client finds a channel that went dead.
const pingEvery = 15 * time.Second

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, errors.New("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	sub, cancel := s.app.Bus.Subscribe(actor(r).Workspace)
	defer cancel()
	// A revoked device loses its stream at once, a session that ended at the next ping.
	ctx, release := s.app.Auth.Hold(r.Context(), actor(r).Device)
	defer release()
	alive := s.alive(r)
	if !alive(ctx) {
		return
	}
	fmt.Fprint(w, ": ok\n\n")
	flusher.Flush()
	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			if !alive(ctx) {
				return
			}
			fmt.Fprint(w, "event: ping\ndata: {}\n\n")
			flusher.Flush()
		case <-sub.Ready():
			for _, e := range sub.Take() {
				b, _ := json.Marshal(e)
				fmt.Fprintf(w, "data: %s\n\n", b)
			}
			flusher.Flush()
		}
	}
}

func send(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

// themePicture is a picture deck ships with a theme, such as the clouds of juce.
func (s *Server) themePicture(w http.ResponseWriter, r *http.Request) {
	b := theme.Shipped(r.PathValue("name"), r.PathValue("file"))
	if b == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/webp")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(b)
}

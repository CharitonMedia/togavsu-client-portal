// Package portal is the Client Portal module's server: the client pages, the staff
// Messages inbox, matter tab, widget, badge and search, the Portal access panel, and a
// JSON endpoint for every action under /api/.
//
// The module keeps no user, password or session. Every request carries the core's
// context token for the viewer (the frame's ?context=, a context form field, or the
// X-Togavsu-Context-Token header); the module verifies it with the core and makes every
// core call with its own token plus that context token, so the core's walls, roles and
// attorney-only rules decide what the viewer sees.
package portal

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu-client-portal/internal/coreapi"
)

// ID is the module id.
const ID = "client-portal"

// Config is what the container gets: the core's address, its token and its own DB
// login (search_path set to mod_client_portal by the core).
type Config struct {
	Core  string
	Token string
	DB    *sql.DB
	HTTP  *http.Client
	Now   func() time.Time
	Log   func(format string, args ...any)
}

// Server is the module's HTTP server.
type Server struct {
	c       Config
	api     *coreapi.Client
	uploads *uploadRegistry
}

// New returns the server.
func New(c Config) *Server {
	if c.HTTP == nil {
		c.HTTP = http.DefaultClient
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Log == nil {
		c.Log = log.Printf
	}
	return &Server{c: c, api: &coreapi.Client{Base: c.Core, Token: c.Token, HTTP: c.HTTP}, uploads: newUploadRegistry()}
}

func (s *Server) logf(format string, args ...any) { s.c.Log(format, args...) }

func (s *Server) now() int64 { return s.c.Now().UTC().Unix() }

//go:embed static
var staticFS embed.FS

// Handler serves every page and endpoint.
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	m.HandleFunc("GET /static/{file}", s.static)
	m.HandleFunc("GET /api/openapi.json", s.openapi)

	// Client pages.
	m.HandleFunc("GET /client/home", s.clientPage(s.homePage))
	m.HandleFunc("GET /client/access-ended", s.clientPage(s.accessEndedPage))
	m.HandleFunc("GET /client/pick-matter", s.clientPage(s.pickMatterPage))
	m.HandleFunc("POST /client/active-matter", s.clientPage(s.pickMatterPost))
	m.HandleFunc("GET /client/documents", s.clientPage(s.documentsPage))
	m.HandleFunc("GET /client/documents/{file_id}/view", s.clientPage(s.viewDocument))
	m.HandleFunc("POST /client/documents/archive", s.clientPage(s.archivePost))
	m.HandleFunc("POST /client/upload", s.clientPage(s.uploadPost))
	m.HandleFunc("GET /client/invoices", s.clientPage(s.invoicesPage))
	m.HandleFunc("GET /client/messages", s.clientPage(s.clientMessagesPage))
	m.HandleFunc("POST /client/messages", s.clientPage(s.clientNewThreadPost))
	m.HandleFunc("POST /client/messages/{thread_id}", s.clientPage(s.clientReplyPost))
	m.HandleFunc("GET /client/account", s.clientPage(s.accountPage))
	m.HandleFunc("POST /client/account", s.clientPage(s.accountPost))

	// Staff pages.
	m.HandleFunc("GET /staff/inbox", s.staffPage(s.inboxPage))
	m.HandleFunc("GET /staff/matter-messages", s.staffPage(s.matterMessagesPage))
	m.HandleFunc("GET /staff/thread/{thread_id}", s.staffPage(s.staffThreadPage))
	m.HandleFunc("POST /staff/threads", s.staffPage(s.staffNewThreadPost))
	m.HandleFunc("POST /staff/threads/{thread_id}/messages", s.staffPage(s.staffReplyPost))
	m.HandleFunc("POST /staff/threads/{thread_id}/status", s.staffPage(s.staffStatusPost))
	m.HandleFunc("POST /staff/messages/{message_id}/retract", s.staffPage(s.staffRetractPost))
	m.HandleFunc("GET /staff/new", s.staffPage(s.newMessagePage))
	m.HandleFunc("GET /staff/widget", s.staffPage(s.widgetPage))
	m.HandleFunc("GET /staff/badge", s.staffPage(s.badge))
	m.HandleFunc("GET /staff/search", s.staffPage(s.searchPage))
	m.HandleFunc("GET /staff/access", s.staffPage(s.accessPage))
	m.HandleFunc("POST /staff/access/{contact_id}/{action}", s.staffPage(s.accessPost))

	// JSON endpoints for agents and for the pages' scripts.
	m.HandleFunc("GET /api/threads", s.anyAPI(s.apiListThreads))
	m.HandleFunc("POST /api/threads", s.anyAPI(s.apiStartThread))
	m.HandleFunc("GET /api/threads/{thread_id}", s.anyAPI(s.apiGetThread))
	m.HandleFunc("POST /api/threads/{thread_id}/messages", s.anyAPI(s.apiReply))
	m.HandleFunc("POST /api/threads/{thread_id}/status", s.staffAPI(s.apiThreadStatus))
	m.HandleFunc("POST /api/messages/{message_id}/retract", s.staffAPI(s.apiRetract))
	m.HandleFunc("GET /api/unread", s.staffAPI(s.apiUnread))
	m.HandleFunc("GET /api/search", s.staffAPI(s.apiSearch))
	m.HandleFunc("POST /api/active-matter", s.clientAPI(s.apiActiveMatter))
	m.HandleFunc("POST /api/uploads", s.clientAPI(s.apiCreateUpload))
	m.HandleFunc("PUT /api/uploads/{upload_id}", s.clientAPI(s.apiPutChunk))
	m.HandleFunc("POST /api/uploads/{upload_id}/complete", s.clientAPI(s.apiCompleteUpload))
	m.HandleFunc("POST /api/archives", s.clientAPI(s.apiArchive))
	m.HandleFunc("GET /api/access", s.staffAPI(s.apiListAccess))
	m.HandleFunc("POST /api/access/{contact_id}/{action}", s.staffAPI(s.apiAccessAction))

	m.HandleFunc("OPTIONS /", s.preflight)
	return cors(m)
}

// cors lets the sandboxed frame (an opaque origin) call the module through the core's
// /m/ proxy. Requests carry the context token, never a cookie, so allowing any origin
// adds nothing a token holder doesn't already have.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Expose-Headers", "Retry-After")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) preflight(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Range, X-Togavsu-Context-Token, HX-Request, HX-Current-URL, HX-Target, HX-Trigger, HX-Trigger-Name, HX-Boosted")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	types := map[string]string{"htmx.min.js": "text/javascript", "portal.js": "text/javascript", "portal.css": "text/css", "htmx-LICENSE.txt": "text/plain"}
	ct, ok := types[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	b, err := staticFS.ReadFile("static/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ct+"; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(b)
}

// --- viewer ---

// viewer is the verified user behind a request.
type viewer struct {
	Token    string
	UserID   string
	Roles    []string
	MatterID string

	me       *Me
	matters  map[string]Matter
	missing  map[string]bool
	contacts map[string]Contact
}

func (v *viewer) has(roles ...string) bool {
	for _, have := range v.Roles {
		for _, want := range roles {
			if have == want {
				return true
			}
		}
	}
	return false
}

// isClient is a client-only user: the client role and no firm role.
func (v *viewer) isClient() bool {
	return v.has("client") && !v.has("admin", "attorney", "staff", "accountant")
}

// isStaff is a firm user who works with client messages.
func (v *viewer) isStaff() bool { return v.has("admin", "attorney", "staff") }

// errNoToken is a request with no usable context token.
var errNoToken = errors.New("client-portal: no valid context token")

func contextToken(r *http.Request) string {
	if t := r.Header.Get(coreapi.ContextHeader); t != "" {
		return t
	}
	if t := r.URL.Query().Get("context"); t != "" {
		return t
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		return r.PostFormValue("context")
	}
	return ""
}

// auth verifies the context token with the core. A forged, expired or foreign token
// (one issued for another module) is errNoToken.
func (s *Server) auth(r *http.Request) (*viewer, error) {
	tok := contextToken(r)
	if tok == "" {
		return nil, errNoToken
	}
	var out struct {
		UserID   string   `json:"viewer_user_id"`
		Roles    []string `json:"roles"`
		Module   string   `json:"module"`
		MatterID *string  `json:"matter_id"`
	}
	err := s.api.Post(r.Context(), tok, "/api/v1/modules/"+ID+"/context-tokens/verify", nil, &out)
	if st := coreapi.StatusOf(err); st == http.StatusUnauthorized || st == http.StatusForbidden {
		return nil, errNoToken
	}
	if err != nil {
		return nil, err
	}
	if out.UserID == "" || (out.Module != "" && out.Module != ID) {
		return nil, errNoToken
	}
	v := &viewer{Token: tok, UserID: out.UserID, Roles: out.Roles, matters: map[string]Matter{},
		missing: map[string]bool{}, contacts: map[string]Contact{}}
	if out.MatterID != nil {
		v.MatterID = *out.MatterID
	}
	return v, nil
}

type handler func(w http.ResponseWriter, r *http.Request, v *viewer)

// clientPage serves a page only to a client-only user; anyone else gets 404.
func (s *Server) clientPage(h handler) http.HandlerFunc {
	return s.guard(h, (*viewer).isClient, false)
}

// staffPage serves a page only to admin, attorney and staff users; a client (or an
// accountant) gets 404.
func (s *Server) staffPage(h handler) http.HandlerFunc {
	return s.guard(h, (*viewer).isStaff, false)
}

func (s *Server) clientAPI(h handler) http.HandlerFunc { return s.guard(h, (*viewer).isClient, true) }
func (s *Server) staffAPI(h handler) http.HandlerFunc  { return s.guard(h, (*viewer).isStaff, true) }
func (s *Server) anyAPI(h handler) http.HandlerFunc {
	return s.guard(h, func(v *viewer) bool { return v.isClient() || v.isStaff() }, true)
}

func (s *Server) guard(h handler, allowed func(*viewer) bool, api bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := s.auth(r)
		if errors.Is(err, errNoToken) {
			if api {
				writeJSONError(w, &apiError{Status: http.StatusUnauthorized, Code: "unauthenticated", Message: "The context token is invalid or expired."})
				return
			}
			s.renderExpired(w, r)
			return
		}
		if err != nil {
			s.fail(w, r, api, err)
			return
		}
		if !allowed(v) {
			if api {
				writeJSONError(w, errNotFound)
			} else {
				http.NotFound(w, r)
			}
			return
		}
		h(w, r, v)
	}
}

// --- errors ---

// apiError is an answer the module gives a viewer.
type apiError struct {
	Status     int    `json:"status"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	RetryAfter int64  `json:"retry_after,omitempty"`
}

func (e *apiError) Error() string { return e.Message }

var errNotFound = &apiError{Status: http.StatusNotFound, Code: "not_found", Message: "Not found."}

func badRequest(msg string) *apiError {
	return &apiError{Status: http.StatusBadRequest, Code: "validation_failed", Message: msg}
}

func conflict(msg string) *apiError {
	return &apiError{Status: http.StatusConflict, Code: "conflict", Message: msg}
}

// asAPIError maps an error to what the viewer is told. A core 404 stays 404; anything
// else from the core is the core being unreachable.
func asAPIError(err error) *apiError {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae
	}
	switch st := coreapi.StatusOf(err); st {
	case http.StatusNotFound:
		return errNotFound
	case http.StatusUnauthorized:
		return &apiError{Status: st, Code: "unauthenticated", Message: "The context token is invalid or expired."}
	case http.StatusForbidden:
		code := coreapi.CodeOf(err)
		if code == "" {
			code = "forbidden"
		}
		return &apiError{Status: st, Code: code, Message: "That isn't allowed."}
	}
	return &apiError{Status: http.StatusBadGateway, Code: "core_unavailable", Message: "The portal can't reach the firm's system right now. Try again in a moment."}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, e *apiError) {
	if e.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt(e.RetryAfter, 10))
	}
	writeJSON(w, e.Status, e)
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, api bool, err error) {
	e := asAPIError(err)
	if e.Status >= http.StatusInternalServerError {
		s.logf("client-portal: %s %s: %v", r.Method, r.URL.Path, err)
	}
	if api {
		writeJSONError(w, e)
		return
	}
	if e.Status == http.StatusNotFound {
		http.NotFound(w, r)
		return
	}
	if e.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt(e.RetryAfter, 10))
	}
	renderMessage(w, e.Status, e.Message)
}

func decodeJSON(r *http.Request, out any) *apiError {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxJSONBody))
	if err := dec.Decode(out); err != nil {
		return badRequest("The request body isn't valid JSON.")
	}
	return nil
}

// maxJSONBody bounds a JSON request body (a message is at most message_max_chars).
const maxJSONBody = 1 << 20

// --- settings ---

// Settings are the module settings the admin sets on the settings form.
type Settings struct {
	UploadMaxFileMB           int64
	UploadDailyMB             int64
	UploadDailyFiles          int64
	UploadRatePer5Min         int64
	MessageMaxChars           int
	EmailClientOnStaffMessage bool
	WelcomeText               string
}

// settings reads the module's settings with its own token. A setting the core doesn't
// return is the manifest default.
func (s *Server) settings(ctx context.Context) (Settings, error) {
	var rows []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := s.api.Get(ctx, "", "/api/v1/modules/"+ID+"/settings", &rows); err != nil {
		return Settings{}, err
	}
	vals := map[string]string{"upload_max_file_mb": "250", "upload_daily_mb": "2048", "upload_daily_files": "100",
		"upload_rate_per_5min": "90", "message_max_chars": "10000", "email_client_on_staff_message": "true", "welcome_text": ""}
	for _, r := range rows {
		vals[r.Key] = r.Value
	}
	num := func(k string) int64 {
		n, err := strconv.ParseInt(strings.TrimSpace(vals[k]), 10, 64)
		if err != nil {
			return 0
		}
		return n
	}
	return Settings{UploadMaxFileMB: num("upload_max_file_mb"), UploadDailyMB: num("upload_daily_mb"),
		UploadDailyFiles: num("upload_daily_files"), UploadRatePer5Min: num("upload_rate_per_5min"),
		MessageMaxChars: int(num("message_max_chars")), EmailClientOnStaffMessage: strings.TrimSpace(vals["email_client_on_staff_message"]) == "true",
		WelcomeText: vals["welcome_text"]}, nil
}

// --- ids ---

// newID returns a UUIDv7, matching core ids.
func newID(now time.Time) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	ms := uint64(now.UnixMilli())
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], ms)
	copy(b[0:6], ts[2:8])
	b[6] = (b[6] & 0x0f) | 0x70
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// validID reports a UUID-shaped id, so a guessed path segment never reaches the core
// or the database as anything else.
func validID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
				return false
			}
		}
	}
	return true
}

// --- links ---

// link builds a module page path carrying the viewer's context token.
func link(v *viewer, path string, params ...string) string {
	q := url.Values{}
	for i := 0; i+1 < len(params); i += 2 {
		if params[i+1] != "" {
			q.Set(params[i], params[i+1])
		}
	}
	q.Set("context", v.Token)
	return "/m/" + ID + path + "?" + q.Encode()
}

// portalPath is a core /portal/ address of a module's client tab (CORE-10).
func portalPath(module, tab string, params ...string) string {
	q := url.Values{}
	for i := 0; i+1 < len(params); i += 2 {
		if params[i+1] != "" {
			q.Set(params[i], params[i+1])
		}
	}
	p := "/portal/" + module + "/" + tab
	if len(q) > 0 {
		p += "?" + q.Encode()
	}
	return p
}

func money(cents int64) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	return fmt.Sprintf("%s$%d.%02d", sign, cents/100, cents%100)
}

func itoa(n int) string { return strconv.Itoa(n) }

// requestJSON is a viewer call with a JSON body.
func requestJSON(method, path, viewer string, body any) coreapi.Request {
	return coreapi.Request{Method: method, Path: path, Viewer: viewer, JSON: body}
}

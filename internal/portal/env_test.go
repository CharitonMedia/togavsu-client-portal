package portal_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu-client-portal/internal/dbtest"
	"github.com/CharitonMedia/togavsu-client-portal/internal/fakecore"
	"github.com/CharitonMedia/togavsu-client-portal/internal/portal"
)

// env is the client portal running against the fake core and the test database, with
// the spec's section 8 fixtures. Every record is synthetic.
type env struct {
	t    *testing.T
	fc   *fakecore.Core
	db   *dbtest.DB
	h    http.Handler
	core *httptest.Server
	now  time.Time

	ann, avery, sam, cal      *fakecore.User
	casey, drew, jordan       *fakecore.User
	cCasey, cDrew, cJordan    *fakecore.Contact
	m1, m2, m3, m4            *fakecore.Matter
	f1Shared, f1Plain, f1Atty *fakecore.File
	f1Second, f2, f3, f4      *fakecore.File
	i1, i1Draft, i1Void, i2   *fakecore.Invoice
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db := dbtest.New(t)
	e := &env{t: t, db: db, now: time.Date(2026, 11, 2, 15, 0, 0, 0, time.UTC)}
	e.fc = fakecore.New(db.Admin)
	e.fc.Now = e.clock
	e.core = httptest.NewServer(e.fc.Handler())
	t.Cleanup(e.core.Close)
	srv := portal.New(portal.Config{Core: e.core.URL, Token: e.fc.ModuleToken, DB: db.Module, Now: e.clock, Log: t.Logf})
	e.h = srv.Handler()

	fc := e.fc
	e.ann = fc.AddUser("Ann Admin", "ann@example.test", "admin")
	e.avery = fc.AddUser("Avery Attorney", "avery@example.test", "attorney")
	e.sam = fc.AddUser("Sam Staff", "sam@example.test", "staff")
	e.cal = fc.AddUser("Cal Accountant", "cal@example.test", "accountant")
	e.cCasey = fc.AddContact("Casey Client", "casey@example.test")
	e.cDrew = fc.AddContact("Drew Other", "drew@example.test")
	e.cJordan = fc.AddContact("Jordan Coclient", "jordan@example.test")
	e.casey = e.clientUser(e.cCasey, "casey@example.test")
	e.drew = e.clientUser(e.cDrew, "drew@example.test")
	e.jordan = e.clientUser(e.cJordan, "jordan@example.test")

	e.m1 = fc.AddMatter("2026-0001", "Casey Estate", e.avery.ID, e.sam.ID)
	e.m2 = fc.AddMatter("2026-0002", "Drew Purchase", e.avery.ID)
	e.m3 = fc.AddMatter("2026-0003", "Casey and Jordan Trust", e.avery.ID)
	e.m4 = fc.AddMatter("2026-0004", "Casey Walled Matter", e.avery.ID)
	fc.Lock(func() { e.m4.Wall = []string{e.avery.ID} })
	fc.AddParty(e.m1.ID, e.cCasey.ID, "client")
	fc.AddParty(e.m2.ID, e.cDrew.ID, "client")
	fc.AddParty(e.m3.ID, e.cCasey.ID, "client")
	fc.AddParty(e.m3.ID, e.cJordan.ID, "client")
	fc.AddParty(e.m4.ID, e.cCasey.ID, "client")

	e.f1Shared = fc.AddFile(e.m1.ID, "Engagement letter.pdf", true, false, []byte("%PDF-1.7 engagement"))
	e.f1Second = fc.AddFile(e.m1.ID, "Inventory.pdf", true, false, []byte("%PDF-1.7 inventory"))
	e.f1Plain = fc.AddFile(e.m1.ID, "Draft will.docx", false, false, []byte("PK draft"))
	e.f1Atty = fc.AddFile(e.m1.ID, "Attorney notes.pdf", true, true, []byte("%PDF-1.7 notes"))
	e.f2 = fc.AddFile(e.m2.ID, "Drew deed.pdf", true, false, []byte("%PDF-1.7 deed"))
	e.f3 = fc.AddFile(e.m3.ID, "Trust agreement.pdf", true, false, []byte("%PDF-1.7 trust"))
	e.f4 = fc.AddFile(e.m4.ID, "Walled memo.pdf", true, false, []byte("%PDF-1.7 walled"))

	e.i1 = fc.AddInvoice(e.m1.ID, "INV-101", "sent", 125000, 25000)
	e.i1Draft = fc.AddInvoice(e.m1.ID, "INV-102", "draft", 9900, 0)
	e.i1Void = fc.AddInvoice(e.m1.ID, "INV-103", "void", 5000, 0)
	e.i2 = fc.AddInvoice(e.m2.ID, "INV-201", "sent", 30000, 0)
	return e
}

func (e *env) clock() time.Time { return e.now }

func (e *env) clientUser(c *fakecore.Contact, email string) *fakecore.User {
	u := e.fc.AddUser(c.Name, email, "client")
	e.fc.Lock(func() { u.LinkedContactID = c.ID })
	return u
}

// tok issues a client-portal context token for the user, with an optional matter claim.
func (e *env) tok(u *fakecore.User, matter ...string) string {
	m := ""
	if len(matter) > 0 {
		m = matter[0]
	}
	return e.fc.Token(fakecore.ModuleID, u.ID, m, 0)
}

type resp struct {
	Code   int
	Body   string
	Header http.Header
}

func (r resp) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(r.Body), v); err != nil {
		t.Fatalf("decode %d %q: %v", r.Code, r.Body, err)
	}
}

// do calls the module the way the core's /m/client-portal/ proxy does: the prefix is
// gone and the context token rides on the request.
func (e *env) do(method, path, tok string, body io.Reader, ctype string) resp {
	e.t.Helper()
	path = strings.TrimPrefix(path, "/m/client-portal")
	req := httptest.NewRequest(method, path, body)
	if tok != "" {
		req.Header.Set("X-Togavsu-Context-Token", tok)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	return resp{Code: w.Code, Body: w.Body.String(), Header: w.Header()}
}

// putChunk sends one chunk of a chunked upload.
func (e *env) putChunk(path, tok string, data []byte, contentRange string) resp {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(data))
	req.Header.Set("X-Togavsu-Context-Token", tok)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Range", contentRange)
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	return resp{Code: w.Code, Body: w.Body.String(), Header: w.Header()}
}

func (e *env) get(path, tok string) resp { return e.do(http.MethodGet, path, tok, nil, "") }

func (e *env) postJSON(path, tok string, v any) resp {
	b, _ := json.Marshal(v)
	return e.do(http.MethodPost, path, tok, bytes.NewReader(b), "application/json")
}

func (e *env) postForm(path, tok string, vals url.Values) resp {
	return e.do(http.MethodPost, path, tok, strings.NewReader(vals.Encode()), "application/x-www-form-urlencoded")
}

// upload is the page script's per-file upload.
func (e *env) upload(tok, matter, name string, data []byte) resp {
	q := url.Values{"name": {name}, "size": {strconv.Itoa(len(data))}, "matter_id": {matter}}
	return e.do(http.MethodPost, "/client/upload?"+q.Encode(), tok, bytes.NewReader(data), "application/octet-stream")
}

// coreGet calls the fake core as the module does for a viewer.
func (e *env) coreGet(path, tok string) resp {
	e.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, e.core.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+e.fc.ModuleToken)
	if tok != "" {
		req.Header.Set("X-Togavsu-Context-Token", tok)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{Code: res.StatusCode, Body: string(b), Header: res.Header}
}

func want(t *testing.T, r resp, code int) {
	t.Helper()
	if r.Code != code {
		t.Fatalf("status %d, want %d: %s", r.Code, code, r.Body)
	}
}

// startThread has the user start a thread through the JSON endpoint.
func (e *env) startThread(u *fakecore.User, matter, contact, subject, body string, files ...string) (portal.Thread, portal.Message) {
	e.t.Helper()
	r := e.postJSON("/api/threads", e.tok(u), map[string]any{"matter_id": matter, "contact_id": contact, "subject": subject, "body": body, "file_ids": files})
	want(e.t, r, http.StatusCreated)
	var out struct {
		Thread  portal.Thread  `json:"thread"`
		Message portal.Message `json:"message"`
	}
	r.json(e.t, &out)
	return out.Thread, out.Message
}

func (e *env) replyAs(u *fakecore.User, thread, body string, files ...string) resp {
	return e.postJSON("/api/threads/"+thread+"/messages", e.tok(u), map[string]any{"body": body, "file_ids": files})
}

func (e *env) openAs(u *fakecore.User, thread string) (portal.Thread, []portal.Message) {
	e.t.Helper()
	r := e.get("/api/threads/"+thread, e.tok(u))
	want(e.t, r, http.StatusOK)
	var out struct {
		Thread   portal.Thread    `json:"thread"`
		Messages []portal.Message `json:"messages"`
	}
	r.json(e.t, &out)
	return out.Thread, out.Messages
}

func (e *env) threadIDs(u *fakecore.User, query string) map[string]bool {
	e.t.Helper()
	r := e.get("/api/threads?"+query, e.tok(u))
	want(e.t, r, http.StatusOK)
	var out struct {
		Data []portal.Thread `json:"data"`
	}
	r.json(e.t, &out)
	ids := map[string]bool{}
	for _, th := range out.Data {
		ids[th.ID] = true
	}
	return ids
}

func (e *env) badge(u *fakecore.User) int {
	e.t.Helper()
	r := e.get("/staff/badge", e.tok(u))
	want(e.t, r, http.StatusOK)
	var out struct {
		Count int `json:"count"`
	}
	r.json(e.t, &out)
	return out.Count
}

func (e *env) setActive(u *fakecore.User, matter string) {
	e.t.Helper()
	want(e.t, e.postJSON("/api/active-matter", e.tok(u), map[string]string{"matter_id": matter}), http.StatusOK)
}

func (e *env) dbCount(query string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.Admin.QueryRow(query, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func has(t *testing.T, body string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(body, p) {
			t.Errorf("page lacks %q", p)
		}
	}
}

func lacks(t *testing.T, body string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if strings.Contains(body, p) {
			t.Errorf("page shows %q", p)
		}
	}
}

// guessedID is a well-formed id that names nothing.
const guessedID = "0190a000-0000-7000-8000-ffffffffffff"

// Package acceptance_test is the client portal's Postgres acceptance suite on the real
// composed core (pkg/modulekit/coretest): the signed package is installed and approved
// as the firm's admin, the module is served in process with the secrets and database
// login the core handed over, and every criterion of SPEC section 8 is driven through
// the core's real API, frames, access rules and database. A criterion that needs a
// module-core item the core main doesn't have yet is written in full and skips naming
// that item, so the suite turns green criterion by criterion as module-core lands.
// The fake-core suite in internal/portal stays beside it.
package acceptance_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	clientportal "github.com/CharitonMedia/togavsu-client-portal"
	"github.com/CharitonMedia/togavsu-client-portal/internal/portal"
	"github.com/CharitonMedia/togavsu/pkg/modulekit/coretest"
)

// ID is the module id.
const ID = clientportal.ID

// The people, by their coretest key. coretest signs in one user of every role; Drew and
// Jordan are two more client users the suite adds.
const (
	ann    = coretest.Admin
	avery  = coretest.Attorney // responsible attorney on every matter
	sam    = coretest.Staff
	cal    = coretest.Accountant
	casey  = coretest.ClientRole // coretest's client user, a client party on M1
	drew   = "drew"
	jordan = "jordan"
)

// env is the client portal installed on the composed core with the SPEC section 8
// fixtures. Every record is synthetic.
type env struct {
	t *testing.T
	c *coretest.Core

	mu     sync.Mutex
	h      http.Handler  // the module's handler, rebuilt on each deployment
	offset time.Duration // the module's clock runs this far ahead of the wall clock

	pub  ed25519.PublicKey
	priv ed25519.PrivateKey

	m1, m2, m3, m4         string // matters
	cCasey, cDrew, cJordan string // client contacts
	f1Shared, f1Plain      string // M1 files: shared, and not shared
	f1Atty, f2, f3, f4     string // M1 attorney-only, M2, M3 and M4 shared files
}

// newEnv starts the core, installs and approves the package and makes the fixtures.
func newEnv(t *testing.T) *env {
	t.Helper()
	c := coretest.New(t)
	e := &env{t: t, c: c}
	c.Serve(ID, func(m coretest.Module) http.Handler {
		h := portal.New(portal.Config{Core: m.CoreURL, Token: m.Token, DB: m.DB, Now: e.clock, Log: t.Logf}).Handler()
		e.mu.Lock()
		e.h = h
		e.mu.Unlock()
		return h
	})
	var err error
	e.pub, e.priv, err = ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c.TrustKey("Client Portal test key", e.pub)
	c.Install(e.signed(nil))
	c.ApproveAll(ID)

	e.m1, e.cCasey = c.Open, c.ClientContact
	e.m4 = c.Walled
	e.party(c.Attorney, e.m4, e.cCasey) // only the attorney is inside M4's wall
	e.m2, e.cDrew = c.Matter("Drew Purchase", "drew@client.example", nil)
	e.m3, e.cJordan = c.Matter("Casey and Jordan Trust", "jordan@client.example", nil)
	e.party(c.Admin, e.m3, e.cCasey)
	e.clientUser(drew, "Drew Other", "drew@client.example", e.cDrew)
	e.clientUser(jordan, "Jordan Coclient", "jordan@client.example", e.cJordan)

	e.f1Shared = e.file(e.m1, "Engagement letter.pdf", true, false)
	e.f1Plain = e.file(e.m1, "Draft will.pdf", false, false)
	e.f1Atty = e.file(e.m1, "Attorney notes.pdf", true, true)
	e.f2 = e.file(e.m2, "Drew deed.pdf", true, false)
	e.f3 = e.file(e.m3, "Trust agreement.pdf", true, false)
	e.f4 = e.fileAs(c.Attorney, e.m4, "Walled memo.pdf", true, false)
	return e
}

// signed is the package the package command builds, signed with the suite's key: edit,
// when set, changes a copy of the full package files, then the pending parts the pinned
// core refuses are held back (package/pending.yaml).
func (e *env) signed(edit func(map[string][]byte)) []byte {
	e.t.Helper()
	b, _, err := clientportal.Build(e.priv, edit)
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

// party has by add contact as a client party on matter.
func (e *env) party(by *coretest.User, matter, contact string) {
	e.t.Helper()
	by.Must(http.StatusCreated, "POST", "/api/v1/matter-parties", map[string]any{"matter_id": matter, "contact_id": contact, "role": "client"})
}

// clientUser has the admin create a client user linked to contact, signs it in over
// HTTP and registers it under key. The password is built at runtime and never stored.
func (e *env) clientUser(key, name, email, contact string) {
	e.t.Helper()
	password := runtimePassword(e.t)
	r := e.c.Admin.Must(http.StatusCreated, "POST", "/api/v1/users", map[string]any{"name": name, "email": email, "roles": []string{"client"}, "password": password, "linked_contact_id": contact})
	id, _ := r.JSON["id"].(string)
	cl := e.c.NewClient()
	login := cl.Must(http.StatusOK, "POST", "/api/v1/auth/login", map[string]string{"email": email, "password": password})
	if login.JSON["status"] != "authenticated" {
		e.t.Fatalf("%s sign-in: %s", name, login.Body)
	}
	cl.CSRF, _ = login.JSON["csrf_token"].(string)
	e.c.Users[key] = &coretest.User{Client: cl, ID: id, Role: coretest.ClientRole, Name: name, Email: email}
}

// file has the admin upload a small PDF to matter and publish it.
func (e *env) file(matter, name string, shared, attorneyOnly bool) string {
	e.t.Helper()
	return e.fileAs(e.c.Admin, matter, name, shared, attorneyOnly)
}

// fileAs has by upload a small PDF to matter and publish it.
func (e *env) fileAs(by *coretest.User, matter, name string, shared, attorneyOnly bool) string {
	e.t.Helper()
	body := []byte("%PDF-1.7 synthetic " + name + "\n%%EOF\n")
	sum := sha256.Sum256(body)
	a := by
	up := a.Must(http.StatusCreated, "POST", "/api/v1/file-uploads", map[string]any{"file_name": name, "matter_id": matter, "media_type": "application/pdf", "size_bytes": len(body), "sha256": hex.EncodeToString(sum[:])})
	uid, _ := up.JSON["id"].(string)
	if r := a.Raw("PUT", "/api/v1/file-uploads/"+uid, body, "Content-Type", "application/octet-stream", "Content-Range", "bytes 0-"+strconv.Itoa(len(body)-1)+"/"+strconv.Itoa(len(body))); r.Code != http.StatusOK {
		e.t.Fatalf("upload chunk: %d %s", r.Code, r.Body)
	}
	a.Must(http.StatusOK, "POST", "/api/v1/file-uploads/"+uid+"/complete", nil)
	f := a.Must(http.StatusCreated, "POST", "/api/v1/files", map[string]any{"upload_id": uid, "matter_id": matter, "name": name, "shared_with_client": shared, "attorney_only": attorneyOnly})
	id, _ := f.JSON["id"].(string)
	return id
}

// runtimePassword is a random synthetic password, made at runtime and never stored.
func runtimePassword(t *testing.T) string {
	t.Helper()
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// clock is the module's clock: the wall clock (the core's) plus what advance added,
// so the module's own upload windows can be stepped past.
func (e *env) clock() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return time.Now().Add(e.offset)
}

func (e *env) advance(d time.Duration) {
	e.mu.Lock()
	e.offset += d
	e.mu.Unlock()
}

// settings changes the module's settings as the admin does.
func (e *env) settings(v map[string]string) {
	e.t.Helper()
	e.c.Admin.Must(http.StatusOK, "PUT", "/api/v1/modules/"+ID+"/settings", v)
}

// contact adds a client party with email on matter and returns its id.
func (e *env) contact(matter, family, email string) string {
	e.t.Helper()
	a := e.c.Admin
	r := a.Must(http.StatusCreated, "POST", "/api/v1/contacts", map[string]any{"kind": "person", "given_name": "Client", "family_name": family})
	id, _ := r.JSON["id"].(string)
	a.Must(http.StatusCreated, "POST", "/api/v1/contact-emails", map[string]any{"contact_id": id, "label": "home", "email": email, "primary": true})
	e.party(a, matter, id)
	return id
}

// number is a matter's number, read as the admin.
func (e *env) number(matter string) string {
	e.t.Helper()
	n, _ := e.c.Admin.Must(http.StatusOK, "GET", "/api/v1/matters/"+matter, nil).JSON["number"].(string)
	return n
}

// invoice bills an hour of the attorney's time on matter and leaves the invoice in
// status: draft, sent (approved and sent) or void.
func (e *env) invoice(matter, status string) string {
	e.t.Helper()
	e.c.LogTime(avery, matter, 10)
	drafts := e.c.Attorney.Must(http.StatusCreated, "POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": matter})
	var page struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := jsonInto(drafts.Body, &page); err != nil || len(page.Data) == 0 {
		e.t.Fatalf("invoice draft: %v %s", err, drafts.Body)
	}
	id := page.Data[0].ID
	if status == "draft" {
		return id
	}
	e.c.Attorney.Must(http.StatusOK, "POST", "/api/v1/invoices/"+id+"/approve", nil)
	e.c.Attorney.Must(http.StatusOK, "POST", "/api/v1/invoices/"+id+"/send", map[string]any{"deliver": "file"})
	if status == "void" {
		e.c.Attorney.Must(http.StatusOK, "POST", "/api/v1/invoices/"+id+"/void", map[string]any{"reason": "Synthetic void"})
	}
	return id
}

// invoiceNumber is an invoice's number, read as the admin.
func (e *env) invoiceNumber(id string) string {
	e.t.Helper()
	n, _ := e.c.Admin.Must(http.StatusOK, "GET", "/api/v1/invoices/"+id, nil).JSON["number"].(string)
	return n
}

func jsonInto(body string, v any) error { return json.Unmarshal([]byte(body), v) }

// tok is a client-portal context token for who, with a matter claim when one is given,
// issued by the core the way its frames get one.
func (e *env) tok(who string, matter ...string) string {
	e.t.Helper()
	m := ""
	if len(matter) > 0 {
		m = matter[0]
	}
	return e.c.ContextFor(who, ID, m)
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

// do calls the module in process the way the core's /m/client-portal/ proxy does: the
// prefix is gone and the viewer's context token rides on the request.
func (e *env) do(method, path, tok string, body []byte, ctype string, hdr ...string) resp {
	e.t.Helper()
	e.mu.Lock()
	h := e.h
	e.mu.Unlock()
	if h == nil {
		e.t.Fatal("client-portal is not deployed")
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if tok != "" {
		req.Header.Set(coretest.ContextHeader, tok)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return resp{Code: w.Code, Body: w.Body.String(), Header: w.Header()}
}

func (e *env) get(path, tok string) resp { return e.do(http.MethodGet, path, tok, nil, "") }

func (e *env) postJSON(path, tok string, v any) resp {
	b, _ := json.Marshal(v)
	return e.do(http.MethodPost, path, tok, b, "application/json")
}

func (e *env) postForm(path, tok string, vals url.Values) resp {
	return e.do(http.MethodPost, path, tok, []byte(vals.Encode()), "application/x-www-form-urlencoded")
}

// upload is the page script's per-file upload.
func (e *env) upload(tok, matter, name string, data []byte) resp {
	q := url.Values{"name": {name}, "size": {strconv.Itoa(len(data))}, "matter_id": {matter}}
	return e.do(http.MethodPost, "/client/upload?"+q.Encode(), tok, data, "application/octet-stream")
}

// coreGet calls the core as the module does for a viewer: the module's token and the
// viewer's context token.
func (e *env) coreGet(path, tok string) coretest.Resp {
	e.t.Helper()
	return e.c.ModuleClient(ID).Do("GET", path, nil, coretest.ContextHeader, tok)
}

func want(t *testing.T, r resp, code int) {
	t.Helper()
	if r.Code != code {
		t.Fatalf("status %d, want %d: %s", r.Code, code, r.Body)
	}
}

// thread and message are the module's JSON shapes the suite reads.
type thread struct {
	ID        string `json:"id"`
	MatterID  string `json:"matter_id"`
	ContactID string `json:"contact_id"`
	Status    string `json:"status"`
}

type message struct {
	ID          string  `json:"id"`
	AuthorKind  string  `json:"author_kind"`
	Body        string  `json:"body"`
	Removed     bool    `json:"removed"`
	ReadAt      *string `json:"read_at"`
	Attachments []struct {
		FileID string `json:"file_id"`
	} `json:"attachments"`
}

// startThread has who start a thread through the module's JSON endpoint.
func (e *env) startThread(who, matter, contact, subject, body string, files ...string) (thread, message) {
	e.t.Helper()
	r := e.postJSON("/api/threads", e.tok(who), map[string]any{"matter_id": matter, "contact_id": contact, "subject": subject, "body": body, "file_ids": files})
	want(e.t, r, http.StatusCreated)
	var out struct {
		Thread  thread  `json:"thread"`
		Message message `json:"message"`
	}
	r.json(e.t, &out)
	return out.Thread, out.Message
}

func (e *env) replyAs(who, th, body string, files ...string) resp {
	return e.postJSON("/api/threads/"+th+"/messages", e.tok(who), map[string]any{"body": body, "file_ids": files})
}

func (e *env) openAs(who, th string) (thread, []message) {
	e.t.Helper()
	r := e.get("/api/threads/"+th, e.tok(who))
	want(e.t, r, http.StatusOK)
	var out struct {
		Thread   thread    `json:"thread"`
		Messages []message `json:"messages"`
	}
	r.json(e.t, &out)
	return out.Thread, out.Messages
}

func (e *env) threadIDs(who, query string) map[string]bool {
	e.t.Helper()
	r := e.get("/api/threads?"+query, e.tok(who))
	want(e.t, r, http.StatusOK)
	var out struct {
		Data []thread `json:"data"`
	}
	r.json(e.t, &out)
	ids := map[string]bool{}
	for _, th := range out.Data {
		ids[th.ID] = true
	}
	return ids
}

func (e *env) badge(who string) int {
	e.t.Helper()
	r := e.get("/staff/badge", e.tok(who))
	want(e.t, r, http.StatusOK)
	var out struct {
		Count int `json:"count"`
	}
	r.json(e.t, &out)
	return out.Count
}

func (e *env) setActive(who, matter string) {
	e.t.Helper()
	want(e.t, e.postJSON("/api/active-matter", e.tok(who), map[string]string{"matter_id": matter}), http.StatusOK)
}

// events counts the matter's timeline rows with action, read as the admin.
func (e *env) events(matter, action string) int {
	e.t.Helper()
	return e.c.Count(`SELECT COUNT(*) FROM core.events WHERE matter_id = $1 AND action = $2`, matter, action)
}

func (e *env) messageCount() int {
	e.t.Helper()
	return e.c.Count(`SELECT COUNT(*) FROM mod_client_portal.messages`)
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

// needs skips a criterion that waits on module-core items the core main lacks.
func needs(t *testing.T, items ...string) {
	t.Helper()
	t.Skip("needs " + strings.Join(items, ", "))
}

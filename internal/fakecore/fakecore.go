// Package fakecore is an in-memory stand-in for the Togavsu core API, for the module's
// tests only. It serves the current contract operations the module calls plus the
// module API update items it depends on (CORE-01, 03, 04, 08 to 16, 18, 19, 21),
// exactly as docs/modules/CORE-CHANGES.md in the core repository defines them, with
// the core's access rules: walls, roles, attorney-only files, the client's own matters
// and the client access window. It never reaches the network beyond its own test
// listener and holds only synthetic data.
//
// Where the module needs a core table for its foreign keys, the fake mirrors each
// record id into a stub schema "core" in the test database, so the module's RESTRICT
// keys behave as they do on the real core.
package fakecore

import (
	"archive/zip"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// ModuleID is the module under test.
const ModuleID = "client-portal"

// User is a core user.
type User struct {
	ID, Name, Email    string
	Roles              []string
	Active             bool
	LinkedContactID    string
	MustChangePassword bool
	Password           string
	GrantedAt          int64
	Sessions           int
}

func (u *User) has(roles ...string) bool {
	for _, h := range u.Roles {
		for _, w := range roles {
			if h == w {
				return true
			}
		}
	}
	return false
}

func (u *User) clientOnly() bool {
	return u.has("client") && !u.has("admin", "attorney", "staff", "accountant")
}

// ContactInfo is one row of a contact's primary email, phone and address history.
type ContactInfo struct {
	Email, Phone                                   string
	Line1, Line2, City, Region, PostalCode, Country string
	EffectiveFrom                                  int64
	EffectiveTo                                    int64 // 0 while current
	ActorID                                        string
}

// Contact is a core contact.
type Contact struct {
	ID, Name string
	History  []ContactInfo
}

func (c *Contact) current() *ContactInfo {
	for i := range c.History {
		if c.History[i].EffectiveTo == 0 {
			return &c.History[i]
		}
	}
	return nil
}

// Matter is a core matter. Wall, when set, lists the only firm users who may read it.
type Matter struct {
	ID, Number, Name, Status string
	OpenedOn                 string
	ClosedAt                 int64
	Responsible              string
	Assigned                 []string
	Wall                     []string
}

// Party is a matter party.
type Party struct {
	ID, MatterID, ContactID, Role string
}

// File is a core file record.
type File struct {
	ID, Name, MatterID string
	AttorneyOnly       bool
	Shared             bool
	Source, Origin     string
	UploaderID         string
	VersionID          string
	Bytes              []byte
	MediaType          string
	CreatedAt          int64
	Trashed            bool
}

// Upload is a chunked upload session.
type Upload struct {
	ID, Owner, Name, MatterID, MediaType string
	Size                                 int64
	Data                                 []byte
	Status                               string
}

// Invoice is a core invoice.
type Invoice struct {
	ID, MatterID, Number, Status string
	TotalCents, PaidCents        int64
}

// Replenishment is a trust replenishment request naming a contact.
type Replenishment struct {
	ID, MatterID, ContactID, Message, Status string
	AmountCents                              int64
}

// Appointment is a calendar event.
type Appointment struct {
	Title, Kind, Start, End, Location, MatterID string
}

// Event is a timeline row.
type Event struct {
	Action, RecordType, RecordID, MatterID, Summary string
	ClientVisible                                   bool
	ActorID, ActorKind                              string
}

// Notification is one in-app notification row.
type Notification struct {
	UserID, Kind, Title, Link, MatterID string
	Email                               bool
}

// Mail is one module mail message.
type Mail struct {
	ToUserID, MatterID, Subject, Text, LinkPath string
}

// Provider is the lawpay-docuseal fixture module: whether it is installed and
// enabled, which of the client portal's optional view scopes the admin declined, and
// the rows its views publish.
type Provider struct {
	Installed  bool
	Declined   map[string]bool
	InvoicePay []map[string]any
	MatterPay  []map[string]any
	Signatures []map[string]any
}

// Core is the fake core.
type Core struct {
	mu sync.Mutex

	ModuleToken  string
	ModuleTokens map[string]string // other modules' bearer tokens, by token
	key          []byte
	Now          func() time.Time
	TokenTTL     time.Duration
	DB           *sql.DB
	ChunkSize    int64

	Users          map[string]*User
	Contacts       map[string]*Contact
	Matters        map[string]*Matter
	Parties        []Party
	Files          map[string]*File
	Uploads        map[string]*Upload
	Invoices       map[string]*Invoice
	Trust          map[string]int64 // contact|matter -> cents
	Ledger         map[string][]map[string]any
	Replenishments []Replenishment
	Appointments   []Appointment
	Events         []Event
	Notifications  []Notification
	Mail           []Mail
	Settings       map[string]string
	Firm           map[string]any
	DaysAfterClose int
	Provider       Provider
	OtherNav       []map[string]any

	MailFails        bool
	AccessMailStatus string
	Calls            []string
	seq              int
}

// New returns an empty fake core. db, when set, is an admin connection to the test
// database with the stub core schema.
func New(db *sql.DB) *Core {
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	c := &Core{key: k, Now: time.Now, TokenTTL: 5 * time.Minute, DB: db, ChunkSize: 8 << 20,
		Users: map[string]*User{}, Contacts: map[string]*Contact{}, Matters: map[string]*Matter{}, Files: map[string]*File{},
		Uploads: map[string]*Upload{}, Invoices: map[string]*Invoice{}, Trust: map[string]int64{}, Ledger: map[string][]map[string]any{},
		Settings: map[string]string{}, DaysAfterClose: 14, ModuleTokens: map[string]string{}, AccessMailStatus: "sent",
		Firm: map[string]any{"name": "Example Law Office LLC", "short_name": "Example Law", "phone": "555-0100", "email": "office@example.test",
			"client_portal": map[string]any{"client_header": "#22334d", "label": "Client Portal"}}}
	c.ModuleToken = "tvm_" + c.randHex(12)
	return c
}

func (c *Core) randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// NewID returns a fresh UUID-shaped id.
func (c *Core) NewID() string {
	c.seq++
	return fmt.Sprintf("0190a000-%04x-7000-8000-%012x", c.seq>>16&0xffff, c.seq)
}

func (c *Core) mirror(table, id string) {
	if c.DB == nil {
		return
	}
	if _, err := c.DB.Exec(`INSERT INTO core.`+table+` (id) VALUES ($1) ON CONFLICT DO NOTHING`, id); err != nil {
		panic(err)
	}
}

// AddModule registers another module's bearer token (for foreign context tokens).
func (c *Core) AddModule(id string) string {
	tok := "tvm_" + c.randHex(12)
	c.ModuleTokens[tok] = id
	return tok
}

// AddUser adds a user.
func (c *Core) AddUser(name, email string, roles ...string) *User {
	c.mu.Lock()
	defer c.mu.Unlock()
	u := &User{ID: c.NewID(), Name: name, Email: email, Roles: roles, Active: true, Password: "pw-" + c.randHex(6)}
	c.Users[u.ID] = u
	c.mirror("users", u.ID)
	return u
}

// AddContact adds a contact with a primary email.
func (c *Core) AddContact(name, email string) *Contact {
	c.mu.Lock()
	defer c.mu.Unlock()
	ct := &Contact{ID: c.NewID(), Name: name}
	ct.History = []ContactInfo{{Email: email, Phone: "555-0199", EffectiveFrom: c.Now().Unix()}}
	c.Contacts[ct.ID] = ct
	c.mirror("contacts", ct.ID)
	return ct
}

// AddMatter adds an open matter.
func (c *Core) AddMatter(number, name, responsible string, assigned ...string) *Matter {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := &Matter{ID: c.NewID(), Number: number, Name: name, Status: "open", OpenedOn: c.Now().Format("2006-01-02"), Responsible: responsible, Assigned: assigned}
	c.Matters[m.ID] = m
	c.mirror("matters", m.ID)
	return m
}

// AddParty adds a party.
func (c *Core) AddParty(matter, contact, role string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Parties = append(c.Parties, Party{ID: c.NewID(), MatterID: matter, ContactID: contact, Role: role})
}

// AddFile adds a file on a matter.
func (c *Core) AddFile(matter, name string, shared, attorneyOnly bool, content []byte) *File {
	c.mu.Lock()
	defer c.mu.Unlock()
	f := &File{ID: c.NewID(), Name: name, MatterID: matter, Shared: shared, AttorneyOnly: attorneyOnly, Source: "upload", Origin: "firm",
		VersionID: c.NewID(), Bytes: content, CreatedAt: c.Now().Unix() + int64(c.seq)}
	c.Files[f.ID] = f
	c.mirror("files", f.ID)
	return f
}

// AddInvoice adds an invoice.
func (c *Core) AddInvoice(matter, number, status string, total, paid int64) *Invoice {
	c.mu.Lock()
	defer c.mu.Unlock()
	in := &Invoice{ID: c.NewID(), MatterID: matter, Number: number, Status: status, TotalCents: total, PaidCents: paid}
	c.Invoices[in.ID] = in
	return in
}

// SetTrust sets a client's trust balance on a matter.
func (c *Core) SetTrust(contact, matter string, cents int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Trust[contact+"|"+matter] = cents
}

// Lock runs f under the fake's lock, for tests that change state directly.
func (c *Core) Lock(f func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f()
}

// --- context tokens ---

type claims struct {
	Module string `json:"module"`
	UserID string `json:"user_id"`
	Matter string `json:"matter_id,omitempty"`
	Exp    int64  `json:"exp"`
}

func (c *Core) mac(payload string) string {
	h := hmac.New(sha256.New, c.key)
	h.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// Token issues a context token for the user and module, with an optional matter claim
// and a lifetime (0 means the default).
func (c *Core) Token(module, user, matter string, ttl time.Duration) string {
	if ttl == 0 {
		ttl = c.TokenTTL
	}
	b, _ := json.Marshal(claims{Module: module, UserID: user, Matter: matter, Exp: c.Now().Add(ttl).Unix()})
	p := base64.RawURLEncoding.EncodeToString(b)
	return p + "." + c.mac(p)
}

func (c *Core) verify(tok string) (claims, bool) {
	p, m, ok := strings.Cut(tok, ".")
	if !ok || !hmac.Equal([]byte(m), []byte(c.mac(p))) {
		return claims{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return claims{}, false
	}
	var cl claims
	if json.Unmarshal(raw, &cl) != nil || c.Now().Unix() >= cl.Exp {
		return claims{}, false
	}
	return cl, true
}

// --- sign-in (the core's /login, modelled for MOD-POR-10 and 11) ---

// SignIn checks a password and returns where the core sends the user next.
func (c *Core) SignIn(email, password string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, u := range c.Users {
		if strings.EqualFold(u.Email, email) {
			if !u.Active || u.Password != password {
				return "", fmt.Errorf("sign-in refused")
			}
			u.Sessions++
			switch {
			case u.MustChangePassword:
				return "/portal/security", nil
			case u.clientOnly():
				return "/portal", nil
			}
			return "/practice/matters", nil
		}
	}
	return "", fmt.Errorf("sign-in refused")
}

// ChangePassword is the core's /portal/security forced change.
func (c *Core) ChangePassword(user, password string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	u := c.Users[user]
	u.Password, u.MustChangePassword = password, false
}

// --- access rules ---

func (c *Core) isParty(contact, matter, role string) bool {
	for _, p := range c.Parties {
		if p.MatterID == matter && p.ContactID == contact && (role == "" || p.Role == role) {
			return true
		}
	}
	return false
}

// canRead is the core's matter read rule: a client reads matters where their linked
// contact is a client party, until days_after_close after closing; a firm user reads
// any matter unless a wall leaves them out.
func (c *Core) canRead(u *User, matterID string) bool {
	m, ok := c.Matters[matterID]
	if !ok || u == nil {
		return false
	}
	if u.clientOnly() {
		if u.LinkedContactID == "" || !c.isParty(u.LinkedContactID, m.ID, "client") {
			return false
		}
		if m.Status == "closed" && c.Now().Unix() > m.ClosedAt+int64(c.DaysAfterClose)*86400 {
			return false
		}
		return true
	}
	if !u.has("admin", "attorney", "staff", "accountant") {
		return false
	}
	if m.Wall != nil {
		for _, id := range m.Wall {
			if id == u.ID {
				return true
			}
		}
		return false
	}
	return true
}

func (c *Core) canSeeFile(u *User, f *File) bool {
	if f.Trashed || !c.canRead(u, f.MatterID) {
		return false
	}
	if u.clientOnly() {
		return f.Shared && !f.AttorneyOnly
	}
	if f.AttorneyOnly {
		return u.has("attorney", "admin")
	}
	return true
}

// --- HTTP ---

type principal struct {
	module  string
	user    *User
	service bool
	matter  string
}

func problem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status, "code": code, "detail": detail})
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func page(data any) map[string]any {
	return map[string]any{"data": data, "page": map[string]any{"limit": 100, "next_cursor": nil}}
}

// auth resolves the caller. A module bearer with a context token is a viewer call; a
// module bearer alone is a service call; "user:<id>" is a person's own session.
func (c *Core) auth(w http.ResponseWriter, r *http.Request) (principal, bool) {
	bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if id, ok := strings.CutPrefix(bearer, "user:"); ok {
		u := c.Users[id]
		if u == nil || !u.Active {
			problem(w, 401, "unauthenticated", "no such user")
			return principal{}, false
		}
		return principal{user: u}, true
	}
	module := ""
	if bearer == c.ModuleToken {
		module = ModuleID
	} else if m, ok := c.ModuleTokens[bearer]; ok {
		module = m
	} else {
		problem(w, 401, "unauthenticated", "bad bearer token")
		return principal{}, false
	}
	tok := r.Header.Get("X-Togavsu-Context-Token")
	if tok == "" {
		return principal{module: module, service: true}, true
	}
	cl, ok := c.verify(tok)
	if !ok {
		problem(w, 401, "unauthenticated", "the context token is invalid or expired")
		return principal{}, false
	}
	if cl.Module != module {
		problem(w, 403, "forbidden", "the context token was issued for another module")
		return principal{}, false
	}
	u := c.Users[cl.UserID]
	if u == nil || !u.Active {
		problem(w, 401, "unauthenticated", "the context token is invalid or expired")
		return principal{}, false
	}
	if u.MustChangePassword && !strings.HasSuffix(r.URL.Path, "/context-tokens/verify") {
		problem(w, 403, "password_change_required", "set a new password first")
		return principal{}, false
	}
	return principal{module: module, user: u, matter: cl.Matter}, true
}

type route func(w http.ResponseWriter, r *http.Request, p principal)

// viewer admits viewer calls (a module bearer plus a context token) and people.
func (c *Core) viewer(h route) http.HandlerFunc { return c.wrap(h, false) }

// either admits viewer calls and service calls (CORE-01).
func (c *Core) either(h route) http.HandlerFunc { return c.wrap(h, true) }

func (c *Core) wrap(h route, service bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.Calls = append(c.Calls, r.Method+" "+r.URL.Path)
		p, ok := c.auth(w, r)
		if !ok {
			return
		}
		if p.service && !service {
			problem(w, 403, "scope_denied", "this operation takes no service call")
			return
		}
		h(w, r, p)
	}
}

// Handler serves the fake core API.
func (c *Core) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("POST /api/v1/modules/{id}/context-tokens", c.viewer(c.issueToken))
	m.HandleFunc("POST /api/v1/modules/{id}/context-tokens/verify", c.viewer(c.verifyToken))
	m.HandleFunc("GET /api/v1/modules/{id}/settings", c.either(c.settings))
	m.HandleFunc("GET /api/v1/modules/{id}/views/{view}", c.viewer(c.readView))
	m.HandleFunc("GET /api/v1/me", c.viewer(c.getMe))
	m.HandleFunc("GET /api/v1/me/trust", c.viewer(c.myTrust))
	m.HandleFunc("GET /api/v1/me/trust-ledger", c.viewer(c.myLedger))
	m.HandleFunc("GET /api/v1/me/appointments", c.viewer(c.myAppointments))
	m.HandleFunc("GET /api/v1/me/contact", c.viewer(c.myContact))
	m.HandleFunc("PATCH /api/v1/me/contact", c.viewer(c.patchMyContact))
	m.HandleFunc("GET /api/v1/matters", c.viewer(c.listMatters))
	m.HandleFunc("GET /api/v1/matters/{id}", c.viewer(c.getMatter))
	m.HandleFunc("GET /api/v1/matter-parties", c.viewer(c.listParties))
	m.HandleFunc("GET /api/v1/contacts/{id}", c.viewer(c.getContact))
	m.HandleFunc("GET /api/v1/user-directory", c.viewer(c.directory))
	m.HandleFunc("GET /api/v1/files", c.viewer(c.listFiles))
	m.HandleFunc("GET /api/v1/files/{id}", c.viewer(c.getFile))
	m.HandleFunc("PATCH /api/v1/files/{id}", c.viewer(c.patchFile))
	m.HandleFunc("GET /api/v1/files/{id}/versions/{vid}/download", c.viewer(c.download))
	m.HandleFunc("POST /api/v1/file-uploads", c.viewer(c.createUpload))
	m.HandleFunc("PUT /api/v1/file-uploads/{id}", c.viewer(c.putChunk))
	m.HandleFunc("POST /api/v1/file-uploads/{id}/complete", c.viewer(c.completeUpload))
	m.HandleFunc("POST /api/v1/files", c.viewer(c.publishFile))
	m.HandleFunc("POST /api/v1/file-archives", c.viewer(c.archive))
	m.HandleFunc("GET /api/v1/invoices", c.viewer(c.listInvoices))
	m.HandleFunc("GET /api/v1/invoices/{id}", c.viewer(c.getInvoice))
	m.HandleFunc("GET /api/v1/firm-profile", c.viewer(c.firm))
	m.HandleFunc("GET /api/v1/nav/client-context", c.viewer(c.nav))
	m.HandleFunc("POST /api/v1/events", c.viewer(c.createEvent))
	m.HandleFunc("POST /api/v1/notifications", c.either(c.createNotification))
	m.HandleFunc("POST /api/v1/mail/messages", c.either(c.sendMail))
	m.HandleFunc("GET /api/v1/client-access", c.viewer(c.getAccess))
	m.HandleFunc("POST /api/v1/client-access", c.viewer(c.grantAccess))
	m.HandleFunc("POST /api/v1/client-access/{uid}/{action}", c.viewer(c.accessAction))
	return m
}

func (c *Core) issueToken(w http.ResponseWriter, r *http.Request, p principal) {
	if p.module != "" {
		problem(w, 403, "forbidden", "a context token is issued to a signed-in user")
		return
	}
	var body struct {
		MatterID string `json:"matter_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.MatterID != "" && !c.canRead(p.user, body.MatterID) {
		problem(w, 404, "not_found", "matter")
		return
	}
	reply(w, 201, map[string]any{"token": c.Token(r.PathValue("id"), p.user.ID, body.MatterID, 0), "viewer_user_id": p.user.ID, "roles": p.user.Roles})
}

func (c *Core) verifyToken(w http.ResponseWriter, r *http.Request, p principal) {
	if p.module != r.PathValue("id") || p.user == nil {
		problem(w, 403, "forbidden", "a module verifies only its own context tokens")
		return
	}
	var matter any
	if p.matter != "" {
		matter = p.matter
	}
	reply(w, 200, map[string]any{"viewer_user_id": p.user.ID, "roles": p.user.Roles, "module": p.module, "matter_id": matter})
}

func (c *Core) settings(w http.ResponseWriter, r *http.Request, p principal) {
	if p.module != r.PathValue("id") {
		problem(w, 403, "forbidden", "own settings only")
		return
	}
	keys := make([]string, 0, len(c.Settings))
	for k := range c.Settings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rows := []map[string]string{}
	for _, k := range keys {
		rows = append(rows, map[string]string{"key": k, "value": c.Settings[k]})
	}
	reply(w, 200, rows)
}

// readView serves the lawpay-docuseal fixture's published views: 404 when it isn't
// installed, 403 when the admin declined the client portal's scope for the view, and
// otherwise the rows whose keyed record the viewer may read.
func (c *Core) readView(w http.ResponseWriter, r *http.Request, p principal) {
	if r.PathValue("id") != "lawpay-docuseal" || !c.Provider.Installed {
		problem(w, 404, "not_found", "module")
		return
	}
	view := r.PathValue("view")
	if c.Provider.Declined[view] {
		problem(w, 403, "scope_denied", "scope declined")
		return
	}
	ids := map[string]bool{}
	for _, id := range r.URL.Query()["record_id"] {
		ids[id] = true
	}
	var rows []map[string]any
	var key string
	var visible func(id string) bool
	switch view {
	case "invoice_payability":
		rows, key = c.Provider.InvoicePay, "invoice_id"
		visible = func(id string) bool { in := c.Invoices[id]; return in != nil && c.clientInvoice(p.user, in) }
	case "matter_payability":
		rows, key = c.Provider.MatterPay, "matter_id"
		visible = func(id string) bool { return c.canRead(p.user, id) }
	case "signature_requests":
		rows, key = c.Provider.Signatures, "file_id"
		visible = func(id string) bool { f := c.Files[id]; return f != nil && c.canSeeFile(p.user, f) }
	default:
		problem(w, 404, "not_found", "view")
		return
	}
	out := []map[string]any{}
	for _, row := range rows {
		id, _ := row[key].(string)
		if ids[id] && visible(id) {
			out = append(out, row)
		}
	}
	reply(w, 200, map[string]any{"data": out})
}

func (c *Core) getMe(w http.ResponseWriter, _ *http.Request, p principal) {
	u := p.user
	var linked any
	if u.LinkedContactID != "" {
		linked = u.LinkedContactID
	}
	out := map[string]any{"id": u.ID, "name": u.Name, "email": u.Email, "roles": u.Roles, "active": u.Active, "linked_contact_id": linked}
	if u.clientOnly() {
		ended := 0
		for _, m := range c.Matters {
			if c.isParty(u.LinkedContactID, m.ID, "client") && !c.canRead(u, m.ID) {
				ended++
			}
		}
		out["access_ended_matters"] = ended
	}
	reply(w, 200, out)
}

func (c *Core) clientMatters(u *User) []*Matter {
	var out []*Matter
	for _, m := range c.Matters {
		if c.canRead(u, m.ID) {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OpenedOn != out[j].OpenedOn {
			return out[i].OpenedOn > out[j].OpenedOn
		}
		return out[i].Number > out[j].Number
	})
	return out
}

func (c *Core) myTrust(w http.ResponseWriter, _ *http.Request, p principal) {
	if !p.user.clientOnly() {
		problem(w, 403, "forbidden", "client role")
		return
	}
	out := []map[string]any{}
	for _, m := range c.clientMatters(p.user) {
		reqs := []map[string]any{}
		for _, rq := range c.Replenishments {
			if rq.MatterID == m.ID && rq.ContactID == p.user.LinkedContactID && rq.Status == "active" {
				reqs = append(reqs, map[string]any{"id": rq.ID, "matter_id": rq.MatterID, "amount_cents": rq.AmountCents, "message": rq.Message, "status": rq.Status, "created_at": "2026-11-01T00:00:00Z"})
			}
		}
		out = append(out, map[string]any{"matter_id": m.ID, "balance_cents": c.Trust[p.user.LinkedContactID+"|"+m.ID], "replenishment_requests": reqs})
	}
	reply(w, 200, map[string]any{"data": out})
}

func (c *Core) myLedger(w http.ResponseWriter, r *http.Request, p principal) {
	m := r.URL.Query().Get("matter_id")
	if !p.user.clientOnly() || !c.canRead(p.user, m) {
		problem(w, 404, "not_found", "matter")
		return
	}
	rows := c.Ledger[p.user.LinkedContactID+"|"+m]
	if rows == nil {
		rows = []map[string]any{}
	}
	reply(w, 200, map[string]any{"data": rows})
}

func (c *Core) myAppointments(w http.ResponseWriter, _ *http.Request, p principal) {
	out := []map[string]any{}
	for _, a := range c.Appointments {
		if p.user.clientOnly() && c.canRead(p.user, a.MatterID) && (a.Kind == "client_meeting" || a.Kind == "court_date") {
			out = append(out, map[string]any{"title": a.Title, "kind": a.Kind, "start": a.Start, "end": a.End, "all_day": false, "location": a.Location, "matter_id": a.MatterID})
		}
	}
	reply(w, 200, map[string]any{"data": out})
}

func (c *Core) myContact(w http.ResponseWriter, _ *http.Request, p principal) {
	ct := c.Contacts[p.user.LinkedContactID]
	if !p.user.clientOnly() || ct == nil {
		problem(w, 404, "not_found", "contact")
		return
	}
	cur := ct.current()
	reply(w, 200, map[string]any{"email": cur.Email, "phone": cur.Phone, "mailing_address": map[string]string{"line1": cur.Line1, "line2": cur.Line2,
		"city": cur.City, "region": cur.Region, "postal_code": cur.PostalCode, "country": cur.Country}})
}

// patchMyContact keeps history: the old row gets an end date, a new primary row is
// added, and the timeline shows the change with the client as actor (CORE-13).
func (c *Core) patchMyContact(w http.ResponseWriter, r *http.Request, p principal) {
	ct := c.Contacts[p.user.LinkedContactID]
	if !p.user.clientOnly() || ct == nil {
		problem(w, 404, "not_found", "contact")
		return
	}
	var in struct {
		Email, Phone string
		Address      map[string]string `json:"mailing_address"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || (in.Email != "" && !strings.Contains(in.Email, "@")) {
		problem(w, 400, "validation_failed", "email")
		return
	}
	cur := ct.current()
	next := ContactInfo{Email: in.Email, Phone: in.Phone, Line1: in.Address["line1"], Line2: in.Address["line2"], City: in.Address["city"],
		Region: in.Address["region"], PostalCode: in.Address["postal_code"], Country: in.Address["country"], EffectiveFrom: c.Now().Unix(), ActorID: p.user.ID}
	if next.Email == cur.Email && next.Phone == cur.Phone && next.Line1 == cur.Line1 && next.Line2 == cur.Line2 && next.City == cur.City &&
		next.Region == cur.Region && next.PostalCode == cur.PostalCode && next.Country == cur.Country {
		reply(w, 200, map[string]any{"changed": false})
		return
	}
	cur.EffectiveTo = c.Now().Unix()
	ct.History = append(ct.History, next)
	for _, m := range c.clientMatters(p.user) {
		c.Events = append(c.Events, Event{Action: "contact.updated", RecordType: "contact", RecordID: ct.ID, MatterID: m.ID, Summary: "Contact details updated by the client",
			ClientVisible: true, ActorID: p.user.ID, ActorKind: "client"})
	}
	reply(w, 200, map[string]any{"changed": true})
}

func (c *Core) matterJSON(u *User, m *Matter) map[string]any {
	var closed any
	if m.Status == "closed" {
		closed = time.Unix(m.ClosedAt, 0).UTC().Format("2006-01-02")
	}
	out := map[string]any{"id": m.ID, "number": m.Number, "name": m.Name, "status": m.Status, "practice_area_name": "General",
		"opened_on": m.OpenedOn, "closed_on": closed}
	if resp := c.Users[m.Responsible]; resp != nil {
		out["responsible_attorney_name"] = resp.Name
	}
	if !u.clientOnly() {
		out["responsible_attorney_user_id"] = m.Responsible
		out["assigned_staff_user_ids"] = m.Assigned
	}
	return out
}

func (c *Core) listMatters(w http.ResponseWriter, r *http.Request, p principal) {
	status := r.URL.Query().Get("status")
	out := []map[string]any{}
	for _, m := range c.clientMatters(p.user) {
		if status == "" || m.Status == status {
			out = append(out, c.matterJSON(p.user, m))
		}
	}
	reply(w, 200, page(out))
}

// getMatter also serves include=client-portal.thread_summary from the module's
// published view, inside the viewer's matter access.
func (c *Core) getMatter(w http.ResponseWriter, r *http.Request, p principal) {
	m := c.Matters[r.PathValue("id")]
	if m == nil || !c.canRead(p.user, m.ID) {
		problem(w, 404, "not_found", "matter")
		return
	}
	out := c.matterJSON(p.user, m)
	for _, inc := range r.URL.Query()["include"] {
		if inc != "client-portal.thread_summary" || c.DB == nil {
			continue
		}
		var active, unread sql.NullInt64
		var last sql.NullInt64
		rows := []map[string]any{}
		err := c.DB.QueryRow(`SELECT active_threads, unread_from_client, last_message_at FROM mod_client_portal.thread_summary WHERE matter_id = $1`, m.ID).Scan(&active, &unread, &last)
		if err == nil {
			rows = append(rows, map[string]any{"active_threads": active.Int64, "unread_from_client": unread.Int64, "last_message_at": last.Int64})
		}
		out["included"] = map[string]any{inc: rows}
	}
	reply(w, 200, out)
}

func (c *Core) listParties(w http.ResponseWriter, r *http.Request, p principal) {
	m := r.URL.Query().Get("matter_id")
	if p.user.clientOnly() || !c.canRead(p.user, m) {
		problem(w, 404, "not_found", "matter")
		return
	}
	out := []map[string]any{}
	for _, pt := range c.Parties {
		if pt.MatterID == m {
			out = append(out, map[string]any{"id": pt.ID, "matter_id": pt.MatterID, "contact_id": pt.ContactID, "role": pt.Role, "primary": false})
		}
	}
	reply(w, 200, page(out))
}

func (c *Core) getContact(w http.ResponseWriter, r *http.Request, p principal) {
	ct := c.Contacts[r.PathValue("id")]
	if ct == nil || p.user.clientOnly() {
		problem(w, 404, "not_found", "contact")
		return
	}
	reply(w, 200, map[string]any{"id": ct.ID, "kind": "person", "full_name": ct.Name})
}

// directory is CORE-19: a client caller gets names of active staff users only.
func (c *Core) directory(w http.ResponseWriter, r *http.Request, p principal) {
	out := []map[string]any{}
	for _, id := range r.URL.Query()["ids"] {
		u := c.Users[id]
		if u == nil {
			continue
		}
		if p.user.clientOnly() {
			if u.Active && u.has("admin", "attorney", "staff") {
				out = append(out, map[string]any{"id": u.ID, "name": u.Name})
			}
			continue
		}
		out = append(out, map[string]any{"id": u.ID, "name": u.Name, "roles": u.Roles, "active": u.Active})
	}
	reply(w, 200, page(out))
}

func (c *Core) fileJSON(f *File) map[string]any {
	ext := strings.TrimPrefix(path.Ext(f.Name), ".")
	return map[string]any{"id": f.ID, "created_at": time.Unix(f.CreatedAt, 0).UTC().Format(time.RFC3339), "name": f.Name, "extension": ext,
		"matter_id": f.MatterID, "origin": f.Origin, "source": f.Source, "attorney_only": f.AttorneyOnly, "shared_with_client": f.Shared,
		"current_version_id": f.VersionID, "trashed_at": nil, "locked_fields": []string{}, "warnings": []string{}}
}

func (c *Core) listFiles(w http.ResponseWriter, r *http.Request, p principal) {
	m := r.URL.Query().Get("matter_id")
	if !c.canRead(p.user, m) {
		problem(w, 404, "not_found", "matter")
		return
	}
	var fs []*File
	for _, f := range c.Files {
		if f.MatterID == m && c.canSeeFile(p.user, f) {
			fs = append(fs, f)
		}
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].ID < fs[j].ID })
	out := []map[string]any{}
	for _, f := range fs {
		out = append(out, c.fileJSON(f))
	}
	reply(w, 200, page(out))
}

func (c *Core) getFile(w http.ResponseWriter, r *http.Request, p principal) {
	f := c.Files[r.PathValue("id")]
	if f == nil || !c.canSeeFile(p.user, f) {
		problem(w, 404, "not_found", "file")
		return
	}
	reply(w, 200, c.fileJSON(f))
}

func (c *Core) patchFile(w http.ResponseWriter, r *http.Request, p principal) {
	f := c.Files[r.PathValue("id")]
	if f == nil || !c.canSeeFile(p.user, f) || p.user.clientOnly() {
		problem(w, 404, "not_found", "file")
		return
	}
	var in struct {
		Shared *bool `json:"shared_with_client"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	if in.Shared != nil {
		f.Shared = *in.Shared
	}
	reply(w, 200, c.fileJSON(f))
}

func (c *Core) download(w http.ResponseWriter, r *http.Request, p principal) {
	f := c.Files[r.PathValue("id")]
	if f == nil || !c.canSeeFile(p.user, f) || f.VersionID != r.PathValue("vid") {
		problem(w, 404, "not_found", "file")
		return
	}
	_, _ = w.Write(f.Bytes)
}

func (c *Core) createUpload(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		FileName  string `json:"file_name"`
		MatterID  string `json:"matter_id"`
		MediaType string `json:"media_type"`
		SizeBytes int64  `json:"size_bytes"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.FileName == "" || in.SizeBytes <= 0 {
		problem(w, 400, "validation_failed", "file_name and size_bytes")
		return
	}
	if !c.canRead(p.user, in.MatterID) {
		problem(w, 404, "not_found", "matter")
		return
	}
	u := &Upload{ID: c.NewID(), Owner: p.user.ID, Name: in.FileName, MatterID: in.MatterID, MediaType: in.MediaType, Size: in.SizeBytes, Status: "receiving"}
	c.Uploads[u.ID] = u
	reply(w, 201, c.uploadJSON(u))
}

func (c *Core) uploadJSON(u *Upload) map[string]any {
	return map[string]any{"id": u.ID, "chunk_size_bytes": c.ChunkSize, "next_offset": len(u.Data), "size_bytes": u.Size, "status": u.Status,
		"expires_at": c.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)}
}

func (c *Core) putChunk(w http.ResponseWriter, r *http.Request, p principal) {
	u := c.Uploads[r.PathValue("id")]
	if u == nil || u.Owner != p.user.ID || u.Status != "receiving" {
		problem(w, 404, "not_found", "upload")
		return
	}
	var start, end, total int64
	if _, err := fmt.Sscanf(r.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &total); err != nil || total != u.Size || start != int64(len(u.Data)) || end < start {
		problem(w, 400, "validation_failed", "Content-Range")
		return
	}
	b, _ := io.ReadAll(r.Body)
	if int64(len(b)) != end-start+1 || int64(len(b)) > c.ChunkSize {
		problem(w, 400, "validation_failed", "chunk size")
		return
	}
	u.Data = append(u.Data, b...)
	reply(w, 200, c.uploadJSON(u))
}

func (c *Core) completeUpload(w http.ResponseWriter, r *http.Request, p principal) {
	u := c.Uploads[r.PathValue("id")]
	if u == nil || u.Owner != p.user.ID {
		problem(w, 404, "not_found", "upload")
		return
	}
	if int64(len(u.Data)) != u.Size {
		problem(w, 409, "conflict", "incomplete")
		return
	}
	u.Status = "staged"
	reply(w, 200, c.uploadJSON(u))
}

// publishFile makes a staged upload a file. A client-only viewer's file is a client
// upload, origin received, uploaded by the client, even through a module (CORE-16).
func (c *Core) publishFile(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		UploadID string `json:"upload_id"`
		MatterID string `json:"matter_id"`
		Name     string `json:"name"`
		Shared   bool   `json:"shared_with_client"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	u := c.Uploads[in.UploadID]
	if u == nil || u.Owner != p.user.ID || u.Status != "staged" {
		problem(w, 404, "not_found", "upload")
		return
	}
	if !c.canRead(p.user, in.MatterID) {
		problem(w, 404, "not_found", "matter")
		return
	}
	name := in.Name
	if name == "" {
		name = u.Name
	}
	f := &File{ID: c.NewID(), Name: name, MatterID: in.MatterID, Shared: in.Shared, Source: "upload", Origin: "firm", UploaderID: p.user.ID,
		VersionID: c.NewID(), Bytes: u.Data, MediaType: u.MediaType, CreatedAt: c.Now().Unix()}
	if p.user.clientOnly() {
		f.Source, f.Origin, f.Shared = "client_upload", "received", true
	}
	u.Status = "published"
	c.Files[f.ID] = f
	c.mirror("files", f.ID)
	reply(w, 201, c.fileJSON(f))
}

func (c *Core) archive(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		FileIDs []string `json:"file_ids"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.FileIDs) == 0 {
		problem(w, 400, "validation_failed", "file_ids")
		return
	}
	var fs []*File
	for _, id := range in.FileIDs {
		f := c.Files[id]
		if f == nil || !c.canSeeFile(p.user, f) {
			problem(w, 404, "not_found", "file")
			return
		}
		fs = append(fs, f)
	}
	w.Header().Set("Content-Type", "application/zip")
	zw := zip.NewWriter(w)
	for _, f := range fs {
		fw, err := zw.Create(f.ID + "-" + f.Name)
		if err != nil {
			return
		}
		_, _ = fw.Write(f.Bytes)
	}
	_ = zw.Close()
}

func (c *Core) clientInvoice(u *User, in *Invoice) bool {
	if !c.canRead(u, in.MatterID) {
		return false
	}
	if u.clientOnly() {
		return in.Status == "sent" || in.Status == "viewed" || in.Status == "paid"
	}
	return true
}

func (c *Core) invoiceJSON(in *Invoice) map[string]any {
	return map[string]any{"id": in.ID, "matter_id": in.MatterID, "number": in.Number, "issued_on": "2026-11-01", "status": in.Status, "lines": []any{},
		"subtotal_cents": in.TotalCents, "adjustment_cents": 0, "interest_cents": 0, "total_cents": in.TotalCents, "amount_paid_cents": in.PaidCents,
		"balance_due_cents": in.TotalCents - in.PaidCents}
}

func (c *Core) listInvoices(w http.ResponseWriter, r *http.Request, p principal) {
	m := r.URL.Query().Get("matter_id")
	if !c.canRead(p.user, m) {
		problem(w, 404, "not_found", "matter")
		return
	}
	var ins []*Invoice
	for _, in := range c.Invoices {
		if in.MatterID == m && c.clientInvoice(p.user, in) {
			ins = append(ins, in)
		}
	}
	sort.Slice(ins, func(i, j int) bool { return ins[i].Number < ins[j].Number })
	out := []map[string]any{}
	for _, in := range ins {
		out = append(out, c.invoiceJSON(in))
	}
	reply(w, 200, page(out))
}

// getInvoice marks a sent invoice viewed when its client opens it.
func (c *Core) getInvoice(w http.ResponseWriter, r *http.Request, p principal) {
	in := c.Invoices[r.PathValue("id")]
	if in == nil || !c.clientInvoice(p.user, in) {
		problem(w, 404, "not_found", "invoice")
		return
	}
	if p.user.clientOnly() && in.Status == "sent" {
		in.Status = "viewed"
	}
	reply(w, 200, c.invoiceJSON(in))
}

func (c *Core) firm(w http.ResponseWriter, _ *http.Request, _ principal) { reply(w, 200, c.Firm) }

func (c *Core) nav(w http.ResponseWriter, _ *http.Request, p principal) {
	if !p.user.clientOnly() {
		problem(w, 403, "forbidden", "client role")
		return
	}
	out := []map[string]any{{"module_id": ModuleID, "tab_id": "home", "title": "Dashboard"}}
	out = append(out, c.OtherNav...)
	reply(w, 200, map[string]any{"data": out})
}

// createEvent appends a timeline row as the viewer. Firm users write on matters they
// read; a client writes client-visible rows on their own readable matter, as the
// client-portal spec section 5 expects.
func (c *Core) createEvent(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Action        string  `json:"action"`
		RecordType    string  `json:"record_type"`
		RecordID      string  `json:"record_id"`
		MatterID      *string `json:"matter_id"`
		Summary       string  `json:"summary"`
		ClientVisible bool    `json:"client_visible"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.Action == "" || in.RecordType == "" || in.RecordID == "" || in.Summary == "" {
		problem(w, 400, "validation_failed", "event")
		return
	}
	m := ""
	if in.MatterID != nil {
		m = *in.MatterID
	}
	if m != "" && !c.canRead(p.user, m) {
		problem(w, 404, "not_found", "matter")
		return
	}
	if p.user.clientOnly() && !in.ClientVisible {
		problem(w, 403, "forbidden", "a client writes client-visible rows only")
		return
	}
	kind := "staff"
	if p.user.clientOnly() {
		kind = "client"
	}
	c.Events = append(c.Events, Event{Action: in.Action, RecordType: in.RecordType, RecordID: in.RecordID, MatterID: m, Summary: in.Summary,
		ClientVisible: in.ClientVisible, ActorID: p.user.ID, ActorKind: kind})
	reply(w, 201, map[string]any{"action": in.Action})
}

var declaredKinds = map[string][]string{"client_message": {"in_app", "email"}, "client_upload": {"in_app"}}

// createNotification is CORE-03: a module's own kinds, audience matter_team on a
// service call, walls dropping excluded recipients, email by each kind's default.
func (c *Core) createNotification(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Kind     string `json:"kind"`
		Title    string `json:"title"`
		Link     string `json:"link"`
		Audience string `json:"audience"`
		MatterID string `json:"matter_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	kind := strings.TrimPrefix(in.Kind, p.module+".")
	chans, ok := declaredKinds[kind]
	if !ok || in.Title == "" || len(in.Title) > 200 || !(strings.HasPrefix(in.Link, "/modules/"+p.module+"/") || strings.HasPrefix(in.Link, "/portal/"+p.module+"/")) {
		problem(w, 400, "validation_failed", "notification")
		return
	}
	m := c.Matters[in.MatterID]
	if in.Audience != "matter_team" || m == nil {
		problem(w, 400, "validation_failed", "audience")
		return
	}
	email := false
	for _, ch := range chans {
		email = email || ch == "email"
	}
	var ids []string
	for _, uid := range append([]string{m.Responsible}, m.Assigned...) {
		u := c.Users[uid]
		if u == nil || !u.Active || !c.canRead(u, m.ID) {
			continue
		}
		c.Notifications = append(c.Notifications, Notification{UserID: uid, Kind: p.module + "." + kind, Title: in.Title, Link: in.Link, MatterID: m.ID, Email: email})
		ids = append(ids, uid)
	}
	reply(w, 201, map[string]any{"created": len(ids), "notification_ids": ids})
}

// sendMail is CORE-04 with to_user_ids.
func (c *Core) sendMail(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		To       []string `json:"to_user_ids"`
		MatterID string   `json:"matter_id"`
		Subject  string   `json:"subject"`
		Text     string   `json:"text"`
		LinkPath string   `json:"link_path"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.To) == 0 || in.Subject == "" || in.Text == "" ||
		!(strings.HasPrefix(in.LinkPath, "/portal/") || strings.HasPrefix(in.LinkPath, "/modules/")) {
		problem(w, 400, "validation_failed", "mail")
		return
	}
	results := []map[string]any{}
	for _, id := range in.To {
		u := c.Users[id]
		switch {
		case u == nil || !u.Active:
			results = append(results, map[string]any{"user_id": id, "status": "skipped"})
		case c.MailFails:
			results = append(results, map[string]any{"user_id": id, "status": "failed", "error": "smtp refused"})
		default:
			c.Mail = append(c.Mail, Mail{ToUserID: id, MatterID: in.MatterID, Subject: in.Subject, Text: in.Text + "\n\nhttps://firm.example.test" + in.LinkPath, LinkPath: in.LinkPath})
			results = append(results, map[string]any{"user_id": id, "status": "sent"})
		}
	}
	if in.MatterID != "" {
		c.Events = append(c.Events, Event{Action: "mail.sent", RecordType: "matter", RecordID: in.MatterID, MatterID: in.MatterID, Summary: "Email sent: " + in.Subject})
	}
	reply(w, 200, map[string]any{"results": results})
}

// --- client access (CORE-14) ---

func (c *Core) accessContact(w http.ResponseWriter, p principal, contact string) bool {
	if !p.user.has("admin", "attorney", "staff") {
		problem(w, 403, "forbidden", "client_access")
		return false
	}
	for _, pt := range c.Parties {
		if pt.ContactID == contact && pt.Role == "client" && c.canRead(p.user, pt.MatterID) {
			return true
		}
	}
	problem(w, 404, "not_found", "contact")
	return false
}

func (c *Core) linkedUser(contact string) *User {
	for _, u := range c.Users {
		if u.LinkedContactID == contact {
			return u
		}
	}
	return nil
}

func (c *Core) accessJSON(u *User) map[string]any {
	return map[string]any{"user_id": u.ID, "email": u.Email, "active": u.Active, "must_change_password": u.MustChangePassword, "last_sign_in_at": nil, "granted_at": nil}
}

func (c *Core) getAccess(w http.ResponseWriter, r *http.Request, p principal) {
	contact := r.URL.Query().Get("contact_id")
	if !c.accessContact(w, p, contact) {
		return
	}
	u := c.linkedUser(contact)
	if u == nil {
		problem(w, 404, "not_found", "client access")
		return
	}
	reply(w, 200, c.accessJSON(u))
}

func (c *Core) grantAccess(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		ContactID string `json:"contact_id"`
		Email     string `json:"email"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	if !c.accessContact(w, p, in.ContactID) {
		return
	}
	u := c.linkedUser(in.ContactID)
	created := false
	if u == nil {
		email := in.Email
		if email == "" {
			email = c.Contacts[in.ContactID].current().Email
		}
		if email == "" {
			problem(w, 400, "validation_failed", "email")
			return
		}
		u = &User{ID: c.NewID(), Name: c.Contacts[in.ContactID].Name, Email: email, Roles: []string{"client"}, Active: true, LinkedContactID: in.ContactID}
		c.Users[u.ID] = u
		c.mirror("users", u.ID)
		created = true
	} else if !u.clientOnly() {
		problem(w, 409, "conflict", "the user holds another role")
		return
	}
	u.Password, u.MustChangePassword = "tmp-"+c.randHex(8), true
	c.Events = append(c.Events, Event{Action: "client_access.granted", RecordType: "user", RecordID: u.ID, Summary: "Portal access granted"})
	reply(w, 200, map[string]any{"user_id": u.ID, "created": created, "email": u.Email, "mail_status": c.AccessMailStatus})
}

func (c *Core) accessAction(w http.ResponseWriter, r *http.Request, p principal) {
	u := c.Users[r.PathValue("uid")]
	if u == nil || u.LinkedContactID == "" || !c.accessContact(w, p, u.LinkedContactID) {
		if u == nil || u.LinkedContactID == "" {
			problem(w, 404, "not_found", "user")
		}
		return
	}
	if !u.clientOnly() {
		problem(w, 409, "conflict", "the user holds another role")
		return
	}
	switch r.PathValue("action") {
	case "temporary-password":
		pw := "tmp-" + c.randHex(8)
		u.Password, u.MustChangePassword, u.Sessions = pw, true, 0
		c.Events = append(c.Events, Event{Action: "client_access.password_reset", RecordType: "user", RecordID: u.ID, Summary: "Temporary password set"})
		reply(w, 200, map[string]any{"temporary_password": pw})
	case "deactivate":
		u.Active = false
		reply(w, 200, c.accessJSON(u))
	case "reactivate":
		u.Active = true
		reply(w, 200, c.accessJSON(u))
	default:
		problem(w, 404, "not_found", "action")
	}
}

// Reset clears recorded traffic between subtests.
func (c *Core) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Events, c.Notifications, c.Mail, c.Calls = nil, nil, nil, nil
}

// Count returns how many recorded events match action.
func (c *Core) Count(action string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.Events {
		if e.Action == action {
			n++
		}
	}
	return n
}

// FileCount is the number of file records.
func (c *Core) FileCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.Files)
}


package acceptance_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	matterclock "github.com/CharitonMedia/togavsu/examples/matter-clock"
	"github.com/CharitonMedia/togavsu/pkg/modulekit"
	"github.com/CharitonMedia/togavsu/pkg/modulekit/coretest"
)

// Access and isolation, and portal access (SPEC section 8, MOD-POR-01 to 12).

// clientReads names what every client-side step needs: the client role's matter reads
// (CORE-11), and GET /api/v1/me as a viewer call, which the module reads the client's
// linked contact from and which the core main serves only to a session (getMe carries
// no module_delegation, and no CORE item lists adding it).
const clientReads = "CORE-11 (and getMe as a viewer call, on no CORE item yet)"

func TestAccessAndIsolation(t *testing.T) {
	e := newEnv(t)

	t.Run("MOD-POR-01", func(t *testing.T) {
		needs(t, clientReads)
		drewThread, drewMsg := e.startThread(drew, e.m2, "", "Closing date", "When do we close?")
		inv2 := e.invoice(e.m2, "sent")
		tok := e.tok(casey)
		r := e.get("/client/pick-matter", tok)
		want(t, r, http.StatusOK)
		has(t, r.Body, e.number(e.m1), e.number(e.m3), e.number(e.m4))
		lacks(t, r.Body, e.number(e.m2), "Drew Purchase")
		for _, path := range []string{
			"/client/documents?matter_id=" + e.m2,
			"/client/invoices?matter_id=" + e.m2,
			"/client/invoices?invoice_id=" + inv2,
			"/client/messages?matter_id=" + e.m2,
			"/client/messages?thread_id=" + drewThread.ID,
			"/client/documents/" + e.f2 + "/view",
			"/client/home?matter_id=" + e.m2,
			"/client/account?matter_id=" + e.m2,
			"/api/threads/" + drewThread.ID,
			"/api/threads?matter_id=" + e.m2,
			"/client/documents/" + guessedID + "/view",
			"/client/invoices?invoice_id=" + guessedID,
			"/api/threads/" + guessedID,
		} {
			if r := e.get(path, tok); r.Code != http.StatusNotFound {
				t.Errorf("GET %s = %d, want 404", path, r.Code)
			}
		}
		for _, p := range []struct {
			path string
			body any
		}{
			{"/api/threads", map[string]any{"matter_id": e.m2, "body": "hello"}},
			{"/api/threads/" + drewThread.ID + "/messages", map[string]any{"body": "hello"}},
			{"/api/threads/" + guessedID + "/messages", map[string]any{"body": "hello"}},
			{"/api/active-matter", map[string]any{"matter_id": e.m2}},
			{"/api/archives", map[string]any{"matter_id": e.m2}},
			{"/api/uploads", map[string]any{"matter_id": e.m2, "file_name": "a.pdf", "size_bytes": 10}},
			{"/api/messages/" + drewMsg.ID + "/retract", map[string]any{}},
		} {
			if r := e.postJSON(p.path, tok, p.body); r.Code != http.StatusNotFound {
				t.Errorf("POST %s = %d, want 404: %s", p.path, r.Code, r.Body)
			}
		}
		if r := e.upload(tok, e.m2, "a.pdf", pdfBytes); r.Code != http.StatusNotFound {
			t.Errorf("upload to M2 = %d, want 404", r.Code)
		}
		if r := e.postForm("/client/documents/archive", tok, url.Values{"matter_id": {e.m2}}); r.Code != http.StatusNotFound {
			t.Errorf("zip of M2 = %d, want 404", r.Code)
		}
		if e.threadIDs(casey, "")[drewThread.ID] {
			t.Error("Casey's thread list holds Drew's M2 thread")
		}
	})

	t.Run("MOD-POR-02", func(t *testing.T) {
		needs(t, clientReads)
		jordanThread, _ := e.startThread(avery, e.m3, e.cJordan, "Jordan only", "Jordan, please sign.")
		caseyThread, _ := e.startThread(casey, e.m3, "", "Casey only", "My question about the trust.")
		for _, c := range []struct{ who, thread string }{{casey, jordanThread.ID}, {jordan, caseyThread.ID}} {
			tok := e.tok(c.who)
			if r := e.get("/api/threads/"+c.thread, tok); r.Code != http.StatusNotFound {
				t.Errorf("%s reading the other client's thread = %d", c.who, r.Code)
			}
			if r := e.get("/client/messages?thread_id="+c.thread, tok); r.Code != http.StatusNotFound {
				t.Errorf("%s opening the other client's thread page = %d", c.who, r.Code)
			}
			if r := e.postJSON("/api/threads/"+c.thread+"/messages", tok, map[string]any{"body": "x", "file_ids": []string{e.f3}}); r.Code != http.StatusNotFound {
				t.Errorf("%s posting or attaching to the other client's thread = %d", c.who, r.Code)
			}
		}
		if r := e.postJSON("/api/threads", e.tok(casey), map[string]any{"matter_id": e.m3, "contact_id": e.cJordan, "body": "as Jordan"}); r.Code != http.StatusNotFound {
			t.Errorf("Casey starting a thread as Jordan = %d, want 404", r.Code)
		}
	})

	t.Run("MOD-POR-03", func(t *testing.T) {
		needs(t, clientReads)
		e.setActive(casey, e.m1)
		tok := e.tok(casey)
		docs := e.get("/client/documents", tok)
		want(t, docs, http.StatusOK)
		has(t, docs.Body, "Engagement letter.pdf")
		lacks(t, docs.Body, "Draft will.pdf", "Attorney notes.pdf")
		picker := e.get("/client/messages", tok)
		want(t, picker, http.StatusOK)
		has(t, picker.Body, e.f1Shared)
		lacks(t, picker.Body, e.f1Plain, e.f1Atty)
		zip := e.postForm("/client/documents/archive", tok, url.Values{"matter_id": {e.m1}})
		want(t, zip, http.StatusOK)
		lacks(t, zip.Body, "Draft will.pdf", "Attorney notes.pdf")
		for _, f := range []string{e.f1Plain, e.f1Atty} {
			r := e.postJSON("/api/threads", tok, map[string]any{"matter_id": e.m1, "body": "see attached", "file_ids": []string{f}})
			want(t, r, http.StatusBadRequest)
			has(t, r.Body, "A document you attached can't be sent.")
		}
	})

	t.Run("MOD-POR-04", func(t *testing.T) {
		needs(t, clientReads)
		sent, draft, void := e.invoice(e.m1, "sent"), e.invoice(e.m1, "draft"), e.invoice(e.m1, "void")
		tok := e.tok(casey)
		r := e.get("/client/invoices?matter_id="+e.m1, tok)
		want(t, r, http.StatusOK)
		has(t, r.Body, e.invoiceNumber(sent))
		lacks(t, r.Body, e.invoiceNumber(draft), e.invoiceNumber(void))
		for _, id := range []string{draft, void} {
			if r := e.get("/client/invoices?invoice_id="+id, tok); r.Code != http.StatusNotFound {
				t.Errorf("opening a draft or void invoice = %d", r.Code)
			}
		}
		want(t, e.get("/client/invoices?invoice_id="+sent+"&matter_id="+e.m1, tok), http.StatusOK)
		if s := e.c.Admin.Must(http.StatusOK, "GET", "/api/v1/invoices/"+sent, nil).JSON["status"]; s != "viewed" {
			t.Errorf("the sent invoice is %v after Casey opened it, want viewed", s)
		}
	})

	t.Run("MOD-POR-06", func(t *testing.T) {
		th, msg := e.startThread(sam, e.m1, e.cCasey, "Staff only", "Staff pages.")
		for _, who := range []string{ann, avery, sam, cal} {
			for _, p := range []string{"/client/home", "/client/documents", "/client/invoices", "/client/messages", "/client/account"} {
				if r := e.get(p, e.tok(who)); r.Code != http.StatusNotFound {
					t.Errorf("%s GET %s = %d, want 404", who, p, r.Code)
				}
			}
			if r := e.postJSON("/api/uploads", e.tok(who), map[string]any{"matter_id": e.m1, "file_name": "a.pdf", "size_bytes": 3}); r.Code != http.StatusNotFound {
				t.Errorf("%s client endpoint = %d", who, r.Code)
			}
		}
		tok := e.tok(casey, e.m1)
		for _, p := range []string{"/staff/inbox", "/staff/matter-messages?matter_id=" + e.m1, "/staff/thread/" + th.ID, "/staff/new",
			"/staff/widget", "/staff/badge", "/staff/search?q=close", "/staff/access", "/api/unread", "/api/search?q=close", "/api/access?matter_id=" + e.m1} {
			if r := e.get(p, tok); r.Code != http.StatusNotFound {
				t.Errorf("client GET %s = %d, want 404", p, r.Code)
			}
		}
		for _, p := range []string{"/staff/threads", "/staff/threads/" + th.ID + "/messages", "/staff/threads/" + th.ID + "/status",
			"/staff/messages/" + msg.ID + "/retract", "/staff/access/" + e.cCasey + "/grant"} {
			if r := e.postForm(p, tok, url.Values{"body": {"x"}, "status": {"closed"}}); r.Code != http.StatusNotFound {
				t.Errorf("client POST %s = %d, want 404", p, r.Code)
			}
		}
		for _, p := range []string{"/api/threads/" + th.ID + "/status", "/api/messages/" + msg.ID + "/retract", "/api/access/" + e.cCasey + "/grant"} {
			if r := e.postJSON(p, tok, map[string]any{"status": "closed", "matter_id": e.m1}); r.Code != http.StatusNotFound {
				t.Errorf("client POST %s = %d, want 404", p, r.Code)
			}
		}
		if r := e.get("/api/threads", e.tok(cal)); r.Code != http.StatusNotFound {
			t.Errorf("accountant thread list = %d, want 404", r.Code)
		}

		needs(t, "CORE-10")
		for _, who := range []string{ann, avery, sam, cal} {
			if r := e.c.As(who).Do("GET", "/portal/client-portal/home", nil); r.Code != http.StatusNotFound {
				t.Errorf("%s at /portal/client-portal/home = %d, want 404", who, r.Code)
			}
		}
	})

	t.Run("MOD-POR-07", func(t *testing.T) {
		foreign := e.matterClockToken(casey)
		for _, tok := range []string{foreign, "forged.token", ""} {
			r := e.get("/client/home", tok)
			want(t, r, http.StatusUnauthorized)
			has(t, r.Body, `data-expired="1"`, "/m/client-portal/static/portal.js")
			if r := e.get("/api/threads", tok); r.Code != http.StatusUnauthorized {
				t.Errorf("API with a bad token = %d, want 401", r.Code)
			}
		}
		// The page script asks the parent frame once and retries once.
		js := e.get("/static/portal.js", "")
		want(t, js, http.StatusOK)
		has(t, js.Body, "togavsu:context.refresh", "_retried")

		// The fresh token the parent frame hands back works by query or form field as
		// well as by header (the core's /m/ proxy drops the header).
		needs(t, "CORE-09", clientReads)
		fresh := e.tok(casey)
		want(t, e.get("/client/home?context="+url.QueryEscape(fresh), ""), http.StatusOK)
		want(t, e.postForm("/client/account", "", url.Values{"context": {fresh}, "phone": {"555-0199"}}), http.StatusOK)
	})

	t.Run("MOD-POR-08", func(t *testing.T) {
		walled, _ := e.startThread(avery, e.m4, e.cCasey, "Walled subject", "Zanzibar settlement figures.")
		before := e.badge(sam)
		if e.threadIDs(sam, "status=all")[walled.ID] {
			t.Error("M4 thread in Sam's thread list")
		}
		if !e.threadIDs(avery, "status=all")[walled.ID] {
			t.Error("Avery, inside M4's wall, doesn't list its thread")
		}
		tok := e.tok(sam)
		for _, p := range []string{"/staff/inbox", "/staff/inbox?status=archived", "/staff/widget", "/staff/search?q=Zanzibar"} {
			r := e.get(p, tok)
			want(t, r, http.StatusOK)
			lacks(t, r.Body, walled.ID, "Walled subject")
		}
		// The same inbox through the core's frame.
		if r := e.c.Tab(sam, ID, "inbox", ""); r.Code != http.StatusOK || strings.Contains(r.Body, "Walled subject") {
			t.Errorf("Sam's inbox tab through the core: %d", r.Code)
		}
		if r := e.get("/staff/thread/"+walled.ID, tok); r.Code != http.StatusNotFound {
			t.Errorf("Sam opening the M4 thread = %d", r.Code)
		}
		// The core won't frame M4's Messages tab for Sam: it issues no context token
		// for a matter outside Sam's wall.
		if r := e.c.As(sam).Do("POST", "/api/v1/modules/"+ID+"/context-tokens", map[string]any{"matter_id": e.m4}); r.Code != http.StatusNotFound {
			t.Errorf("Sam's context token for M4 = %d", r.Code)
		}
		if r := e.c.As(sam).Do("GET", "/modules/"+ID+"/tabs/messages?matter_id="+e.m4, nil); r.Code != http.StatusNotFound {
			t.Errorf("Sam on M4's Messages tab = %d", r.Code)
		}
		var out struct {
			Hits []map[string]any `json:"data"`
		}
		r := e.get("/api/search?q=Zanzibar", tok)
		want(t, r, http.StatusOK)
		r.json(t, &out)
		if len(out.Hits) != 0 {
			t.Errorf("Sam's search finds the M4 thread: %v", out.Hits)
		}

		// A client message on M4 counts for Avery and never for Sam, in the module's
		// badge and in the core's sidebar badge.
		needs(t, clientReads, "CORE-08")
		want(t, e.replyAs(casey, walled.ID, "Zanzibar reply from Casey."), http.StatusCreated)
		if e.badge(avery) < 1 {
			t.Error("Avery's badge misses the M4 message")
		}
		if e.badge(sam) != before {
			t.Error("Sam's badge moved for an M4 message")
		}
		nav := e.c.As(sam).Must(http.StatusOK, "GET", "/api/v1/nav/context", nil)
		if strings.Contains(nav.Body, `"count":1`) {
			t.Errorf("Sam's sidebar badge counts the M4 message: %s", nav.Body)
		}
	})

	t.Run("MOD-POR-05", func(t *testing.T) {
		needs(t, clientReads, "CORE-18", "CORE-10")
		// The window is 14 days after closed_on; the core main's close takes no date,
		// so the matter closes today and the window is set to zero days.
		e.c.Admin.Must(http.StatusOK, "PATCH", "/api/v1/settings/client_access.days_after_close", map[string]any{"value": 0})
		e.c.Admin.Must(http.StatusOK, "POST", "/api/v1/matters/"+e.m1+"/close", map[string]any{"reason": "Synthetic close"})
		tok := e.tok(casey)
		r := e.get("/client/pick-matter", tok)
		want(t, r, http.StatusOK)
		lacks(t, r.Body, e.number(e.m1))
		if r := e.coreGet("/api/v1/matters/"+e.m1, tok); r.Code != http.StatusNotFound {
			t.Errorf("core read of closed M1 = %d", r.Code)
		}
		if r := e.coreGet("/api/v1/files?matter_id="+e.m1, tok); r.Code != http.StatusNotFound {
			t.Errorf("core file list of closed M1 = %d", r.Code)
		}
		for _, m := range []string{e.m3} {
			e.c.Admin.Must(http.StatusOK, "POST", "/api/v1/matters/"+m+"/close", map[string]any{"reason": "Synthetic close"})
		}
		e.c.Attorney.Must(http.StatusOK, "POST", "/api/v1/matters/"+e.m4+"/close", map[string]any{"reason": "Synthetic close"})
		r = e.get("/client/home", e.tok(casey))
		want(t, r, http.StatusOK)
		has(t, r.Body, "Your portal access has ended", "Synthetic Firm LLP")
		e.c.Admin.Must(http.StatusOK, "POST", "/api/v1/matters/"+e.m1+"/reopen", map[string]any{"reason": "Synthetic reopen"})
		r = e.get("/client/home", e.tok(casey))
		want(t, r, http.StatusOK)
		lacks(t, r.Body, "Your portal access has ended")
	})
}

// matterClockToken installs the core's sample module beside client-portal and issues
// who a context token for it, which client-portal must refuse.
func (e *env) matterClockToken(who string) string {
	e.t.Helper()
	e.c.Serve(matterclock.ID, func(m coretest.Module) http.Handler {
		return matterclock.New(matterclock.Config{Core: m.CoreURL, Token: m.Token, WebhookSecret: m.WebhookSecret, DB: m.DB, SkewSeconds: 300}).Handler()
	})
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		e.t.Fatal(err)
	}
	files, err := matterclock.Files()
	if err != nil {
		e.t.Fatal(err)
	}
	signed, err := modulekit.Build(priv, files)
	if err != nil {
		e.t.Fatal(err)
	}
	e.c.TrustKey("Matter Clock sample key", pub)
	e.c.Install(signed)
	e.c.ApproveAll(matterclock.ID)
	return e.c.ContextFor(who, matterclock.ID, "")
}

var passwordRe = regexp.MustCompile(`<code data-temporary-password>([^<]+)</code>`)

func TestPortalAccess(t *testing.T) {
	e := newEnv(t)

	t.Run("MOD-POR-09", func(t *testing.T) {
		needs(t, "CORE-14", "CORE-05")
		pat := e.contact(e.m1, "Newclient", "pat@client.example")
		r := e.postForm("/staff/access/"+pat+"/grant", e.tok(sam, e.m1), nil)
		want(t, r, http.StatusOK)
		has(t, r.Body, "Sent to pat@client.example")
		access := e.coreGet("/api/v1/client-access?contact_id="+pat, e.tok(sam))
		if access.Code != http.StatusOK || access.JSON["must_change_password"] != true {
			t.Fatalf("grant made no client user with a temporary password: %d %s", access.Code, access.Body)
		}
	})

	t.Run("MOD-POR-10", func(t *testing.T) {
		needs(t, "CORE-14", "CORE-10")
		r := e.postForm("/staff/access/"+e.cCasey+"/temporary-password", e.tok(sam, e.m1), nil)
		want(t, r, http.StatusOK)
		m := passwordRe.FindStringSubmatch(r.Body)
		if m == nil {
			t.Fatal("no temporary password shown")
		}
		again := e.get("/staff/access", e.tok(sam, e.m1))
		want(t, again, http.StatusOK)
		lacks(t, again.Body, m[1], "data-temporary-password")
		cl := e.c.NewClient()
		login := cl.Must(http.StatusOK, "POST", "/api/v1/auth/login", map[string]string{"email": e.c.Client.Email, "password": m[1]})
		cl.CSRF, _ = login.JSON["csrf_token"].(string)
		if r := cl.Do("GET", "/portal", nil); r.Loc != "/portal/security" {
			t.Errorf("Casey's next sign-in lands on %q, want /portal/security", r.Loc)
		}
		if r := cl.Do("GET", "/api/v1/matters/"+e.m1, nil); r.Code != http.StatusForbidden {
			t.Errorf("an API call before the password change = %d, want 403", r.Code)
		}
	})

	t.Run("MOD-POR-11", func(t *testing.T) {
		needs(t, "CORE-14")
		tok := e.tok(sam, e.m1)
		want(t, e.postForm("/staff/access/"+e.cCasey+"/deactivate", tok, nil), http.StatusOK)
		has(t, e.get("/staff/access", tok).Body, "Portal off")
		want(t, e.postForm("/staff/access/"+e.cCasey+"/reactivate", tok, nil), http.StatusOK)
		lacks(t, e.get("/staff/access", tok).Body, "Portal off")
		if r := e.c.Admin.Must(http.StatusOK, "GET", "/api/v1/matter-parties?matter_id="+e.m1, nil); !strings.Contains(r.Body, e.cCasey) {
			t.Error("turning access off and on changed Casey's matters")
		}
	})

	t.Run("MOD-POR-12", func(t *testing.T) {
		if r := e.get("/staff/access?matter_id="+e.m1, e.tok(cal)); r.Code != http.StatusNotFound {
			t.Errorf("Cal's panel = %d, want 404", r.Code)
		}
		if r := e.get("/api/access?matter_id="+e.m1, e.tok(cal)); r.Code != http.StatusNotFound {
			t.Errorf("Cal's access API = %d, want 404", r.Code)
		}

		needs(t, "CORE-14")
		r := e.postForm("/staff/access/"+e.cDrew+"/grant", e.tok(sam, e.m1), nil)
		want(t, r, http.StatusBadRequest)
		has(t, r.Body, "That contact isn&#39;t a client on this matter.")
		want(t, e.postJSON("/api/access/"+e.cDrew+"/grant", e.tok(sam), map[string]string{"matter_id": e.m1}), http.StatusBadRequest)
		both := e.contact(e.m1, "Both", "morgan@client.example")
		e.c.Admin.Must(http.StatusCreated, "POST", "/api/v1/users", map[string]any{"name": "Morgan Both", "email": "morgan@client.example",
			"roles": []string{"staff", "client"}, "password": runtimePassword(t), "linked_contact_id": both})
		for _, action := range []string{"grant", "temporary-password", "deactivate"} {
			want(t, e.postJSON("/api/access/"+both+"/"+action, e.tok(sam), map[string]string{"matter_id": e.m1}), http.StatusConflict)
		}
	})
}

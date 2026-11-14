package portal_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu-client-portal/internal/fakecore"
)

// Access and isolation, and portal access (SPEC section 8, MOD-POR-01 to 12).

func TestAccessAndIsolation(t *testing.T) {
	e := newEnv(t)
	drewThread, drewMsg := e.startThread(e.drew, e.m2.ID, "", "Closing date", "When do we close?")

	t.Run("MOD-POR-01", func(t *testing.T) {
		casey := e.tok(e.casey)
		r := e.get("/client/pick-matter", casey)
		want(t, r, http.StatusOK)
		has(t, r.Body, e.m1.Number, e.m3.Number, e.m4.Number)
		lacks(t, r.Body, e.m2.Number, e.m2.Name)
		for _, path := range []string{
			"/client/documents?matter_id=" + e.m2.ID,
			"/client/invoices?matter_id=" + e.m2.ID,
			"/client/invoices?invoice_id=" + e.i2.ID,
			"/client/messages?matter_id=" + e.m2.ID,
			"/client/messages?thread_id=" + drewThread.ID,
			"/client/documents/" + e.f2.ID + "/view",
			"/client/home?matter_id=" + e.m2.ID,
			"/client/account?matter_id=" + e.m2.ID,
			"/api/threads/" + drewThread.ID,
			"/api/threads?matter_id=" + e.m2.ID,
			"/client/documents/" + guessedID + "/view",
			"/client/invoices?invoice_id=" + guessedID,
			"/api/threads/" + guessedID,
		} {
			if r := e.get(path, casey); r.Code != http.StatusNotFound {
				t.Errorf("GET %s = %d, want 404", path, r.Code)
			}
		}
		for _, p := range []struct {
			path string
			body any
		}{
			{"/api/threads", map[string]any{"matter_id": e.m2.ID, "body": "hello"}},
			{"/api/threads/" + drewThread.ID + "/messages", map[string]any{"body": "hello"}},
			{"/api/threads/" + guessedID + "/messages", map[string]any{"body": "hello"}},
			{"/api/active-matter", map[string]any{"matter_id": e.m2.ID}},
			{"/api/archives", map[string]any{"matter_id": e.m2.ID}},
			{"/api/uploads", map[string]any{"matter_id": e.m2.ID, "file_name": "a.pdf", "size_bytes": 10}},
			{"/api/messages/" + drewMsg.ID + "/retract", map[string]any{}},
		} {
			if r := e.postJSON(p.path, casey, p.body); r.Code != http.StatusNotFound {
				t.Errorf("POST %s = %d, want 404: %s", p.path, r.Code, r.Body)
			}
		}
		if r := e.upload(casey, e.m2.ID, "a.pdf", []byte("%PDF-1.7")); r.Code != http.StatusNotFound {
			t.Errorf("upload to M2 = %d, want 404", r.Code)
		}
		if r := e.postForm("/client/messages/"+drewThread.ID, casey, url.Values{"body": {"hi"}}); r.Code != http.StatusNotFound {
			t.Errorf("reply form on M2 thread = %d, want 404", r.Code)
		}
		if r := e.postForm("/client/documents/archive", casey, url.Values{"matter_id": {e.m2.ID}}); r.Code != http.StatusNotFound {
			t.Errorf("zip of M2 = %d, want 404", r.Code)
		}
		if ids := e.threadIDs(e.casey, ""); ids[drewThread.ID] {
			t.Error("Casey's thread list holds Drew's M2 thread")
		}
	})

	t.Run("MOD-POR-02", func(t *testing.T) {
		jordanThread, _ := e.startThread(e.avery, e.m3.ID, e.cJordan.ID, "Jordan only", "Jordan, please sign.")
		caseyThread, _ := e.startThread(e.casey, e.m3.ID, "", "Casey only", "My question about the trust.")
		casey, jordan := e.tok(e.casey), e.tok(e.jordan)
		for _, c := range []struct {
			tok, thread string
		}{{casey, jordanThread.ID}, {jordan, caseyThread.ID}} {
			if r := e.get("/api/threads/"+c.thread, c.tok); r.Code != http.StatusNotFound {
				t.Errorf("read other client's thread = %d", r.Code)
			}
			if r := e.get("/client/messages?thread_id="+c.thread, c.tok); r.Code != http.StatusNotFound {
				t.Errorf("open other client's thread page = %d", r.Code)
			}
			if r := e.postJSON("/api/threads/"+c.thread+"/messages", c.tok, map[string]any{"body": "x", "file_ids": []string{e.f3.ID}}); r.Code != http.StatusNotFound {
				t.Errorf("post or attach to other client's thread = %d", r.Code)
			}
		}
		if r := e.postJSON("/api/threads", casey, map[string]any{"matter_id": e.m3.ID, "contact_id": e.cJordan.ID, "body": "as Jordan"}); r.Code != http.StatusNotFound {
			t.Errorf("Casey starting a thread as Jordan = %d, want 404", r.Code)
		}
		if e.threadIDs(e.casey, "")[jordanThread.ID] || e.threadIDs(e.jordan, "")[caseyThread.ID] {
			t.Error("a client lists the other client's thread")
		}
	})

	t.Run("MOD-POR-03", func(t *testing.T) {
		e.setActive(e.casey, e.m1.ID)
		casey := e.tok(e.casey)
		docs := e.get("/client/documents", casey)
		want(t, docs, http.StatusOK)
		has(t, docs.Body, e.f1Shared.Name)
		lacks(t, docs.Body, e.f1Plain.Name, e.f1Atty.Name)
		picker := e.get("/client/messages", casey)
		want(t, picker, http.StatusOK)
		has(t, picker.Body, e.f1Shared.ID)
		lacks(t, picker.Body, e.f1Plain.ID, e.f1Atty.ID)
		zip := e.postForm("/client/documents/archive", casey, url.Values{"matter_id": {e.m1.ID}})
		want(t, zip, http.StatusOK)
		lacks(t, zip.Body, e.f1Plain.Name, e.f1Atty.Name)
		for _, f := range []*fakecore.File{e.f1Plain, e.f1Atty} {
			r := e.postJSON("/api/threads", casey, map[string]any{"matter_id": e.m1.ID, "body": "see attached", "file_ids": []string{f.ID}})
			want(t, r, http.StatusBadRequest)
			has(t, r.Body, "A document you attached can't be sent.")
		}
	})

	t.Run("MOD-POR-04", func(t *testing.T) {
		casey := e.tok(e.casey)
		r := e.get("/client/invoices?matter_id="+e.m1.ID, casey)
		want(t, r, http.StatusOK)
		has(t, r.Body, e.i1.Number)
		lacks(t, r.Body, e.i1Draft.Number, e.i1Void.Number)
		for _, id := range []string{e.i1Draft.ID, e.i1Void.ID} {
			if r := e.get("/client/invoices?invoice_id="+id, casey); r.Code != http.StatusNotFound {
				t.Errorf("open draft or void invoice = %d", r.Code)
			}
		}
		want(t, e.get("/client/invoices?invoice_id="+e.i1.ID+"&matter_id="+e.m1.ID, casey), http.StatusOK)
		if e.i1.Status != "viewed" {
			t.Errorf("sent invoice is %q after Casey opened it, want viewed", e.i1.Status)
		}
	})

	t.Run("MOD-POR-06", func(t *testing.T) {
		for _, u := range []*fakecore.User{e.ann, e.avery, e.sam, e.cal} {
			for _, p := range []string{"/client/home", "/client/documents", "/client/invoices", "/client/messages", "/client/account"} {
				if r := e.get(p, e.tok(u)); r.Code != http.StatusNotFound {
					t.Errorf("%s GET %s = %d, want 404", u.Name, p, r.Code)
				}
			}
			if r := e.postJSON("/api/uploads", e.tok(u), map[string]any{"matter_id": e.m1.ID, "file_name": "a.pdf", "size_bytes": 3}); r.Code != http.StatusNotFound {
				t.Errorf("%s client endpoint = %d", u.Name, r.Code)
			}
		}
		casey := e.tok(e.casey, e.m1.ID)
		for _, p := range []string{"/staff/inbox", "/staff/matter-messages?matter_id=" + e.m1.ID, "/staff/thread/" + drewThread.ID, "/staff/new",
			"/staff/widget", "/staff/badge", "/staff/search?q=close", "/staff/access", "/api/unread", "/api/search?q=close", "/api/access?matter_id=" + e.m1.ID} {
			if r := e.get(p, casey); r.Code != http.StatusNotFound {
				t.Errorf("client GET %s = %d, want 404", p, r.Code)
			}
		}
		for _, p := range []string{"/staff/threads", "/staff/threads/" + drewThread.ID + "/messages", "/staff/threads/" + drewThread.ID + "/status",
			"/staff/messages/" + drewMsg.ID + "/retract", "/staff/access/" + e.cCasey.ID + "/grant"} {
			if r := e.postForm(p, casey, url.Values{"body": {"x"}, "status": {"closed"}}); r.Code != http.StatusNotFound {
				t.Errorf("client POST %s = %d, want 404", p, r.Code)
			}
		}
		for _, p := range []string{"/api/threads/" + drewThread.ID + "/status", "/api/messages/" + drewMsg.ID + "/retract", "/api/access/" + e.cCasey.ID + "/grant"} {
			if r := e.postJSON(p, casey, map[string]any{"status": "closed", "matter_id": e.m1.ID}); r.Code != http.StatusNotFound {
				t.Errorf("client POST %s = %d, want 404", p, r.Code)
			}
		}
		if r := e.get("/api/threads", e.tok(e.cal)); r.Code != http.StatusNotFound {
			t.Errorf("accountant thread list = %d, want 404", r.Code)
		}
	})

	t.Run("MOD-POR-07", func(t *testing.T) {
		foreign := e.fc.Token("matter-clock", e.casey.ID, "", 0)
		for _, tok := range []string{foreign, "forged.token", ""} {
			r := e.get("/client/home", tok)
			want(t, r, http.StatusUnauthorized)
			has(t, r.Body, `data-expired="1"`, "/m/client-portal/static/portal.js")
			if r := e.get("/api/threads", tok); r.Code != http.StatusUnauthorized {
				t.Errorf("API with a bad token = %d, want 401", r.Code)
			}
		}
		short := e.fc.Token(fakecore.ModuleID, e.casey.ID, "", time.Minute)
		want(t, e.get("/client/home", short), http.StatusOK)
		e.now = e.now.Add(2 * time.Minute)
		want(t, e.get("/client/home", short), http.StatusUnauthorized)
		want(t, e.get("/api/threads", short), http.StatusUnauthorized)
		// The fresh token the parent frame hands back works, by query or form field
		// as well as the header (the core's proxy drops the header).
		fresh := e.tok(e.casey)
		want(t, e.get("/client/home?context="+url.QueryEscape(fresh), ""), http.StatusOK)
		r := e.postForm("/client/account", "", url.Values{"context": {fresh}, "email": {"casey@example.test"}, "phone": {"555-0199"}})
		want(t, r, http.StatusOK)
		// The page script asks the parent frame once and retries once.
		js := e.get("/static/portal.js", "")
		want(t, js, http.StatusOK)
		has(t, js.Body, "togavsu:context.refresh", "_retried")
	})

	t.Run("MOD-POR-08", func(t *testing.T) {
		walled, _ := e.startThread(e.avery, e.m4.ID, e.cCasey.ID, "Walled subject", "Zanzibar settlement figures.")
		e.replyAs(e.casey, walled.ID, "Zanzibar reply from Casey.")
		sam := e.tok(e.sam)
		before := e.badge(e.sam)
		if e.threadIDs(e.sam, "status=all")[walled.ID] {
			t.Error("M4 thread in Sam's thread list")
		}
		for _, p := range []string{"/staff/inbox", "/staff/inbox?status=archived", "/staff/widget", "/staff/search?q=Zanzibar"} {
			r := e.get(p, sam)
			want(t, r, http.StatusOK)
			lacks(t, r.Body, walled.ID, "Walled subject")
		}
		if r := e.get("/staff/thread/"+walled.ID, sam); r.Code != http.StatusNotFound {
			t.Errorf("Sam opening the M4 thread = %d", r.Code)
		}
		if r := e.get("/staff/matter-messages", e.tok(e.sam, e.m4.ID)); r.Code != http.StatusNotFound {
			t.Errorf("Sam on M4's messages tab = %d", r.Code)
		}
		if e.badge(e.avery) < 1 {
			t.Error("Avery's badge misses the M4 message")
		}
		if e.badge(e.sam) != before {
			t.Error("Sam's badge moved for an M4 message")
		}
		var out struct {
			Hits []map[string]any `json:"data"`
		}
		r := e.get("/api/search?q=Zanzibar", sam)
		want(t, r, http.StatusOK)
		r.json(t, &out)
		if len(out.Hits) != 0 {
			t.Errorf("Sam's search finds the M4 thread: %v", out.Hits)
		}
	})

	t.Run("MOD-POR-05", func(t *testing.T) {
		e.fc.Lock(func() { e.m1.Status, e.m1.ClosedAt = "closed", e.now.Unix() })
		e.now = e.now.Add(13 * 24 * time.Hour)
		r := e.get("/client/pick-matter", e.tok(e.casey))
		want(t, r, http.StatusOK)
		has(t, r.Body, e.m1.Number)
		e.now = e.now.Add(2 * 24 * time.Hour) // fifteen days after closing
		casey := e.tok(e.casey)
		r = e.get("/client/pick-matter", casey)
		want(t, r, http.StatusOK)
		lacks(t, r.Body, e.m1.Number)
		if r := e.coreGet("/api/v1/matters/"+e.m1.ID, casey); r.Code != http.StatusNotFound {
			t.Errorf("core read of closed M1 = %d", r.Code)
		}
		if r := e.coreGet("/api/v1/files?matter_id="+e.m1.ID, casey); r.Code != http.StatusNotFound {
			t.Errorf("core file list of closed M1 = %d", r.Code)
		}
		if r := e.get("/client/documents?matter_id="+e.m1.ID, casey); r.Code != http.StatusNotFound {
			t.Errorf("documents of closed M1 = %d", r.Code)
		}
		e.fc.Lock(func() {
			for _, m := range []*fakecore.Matter{e.m3, e.m4} {
				m.Status, m.ClosedAt = "closed", e.now.Add(-20*24*time.Hour).Unix()
			}
		})
		r = e.get("/client/home", e.tok(e.casey))
		want(t, r, http.StatusOK)
		has(t, r.Body, "Your portal access has ended", "Example Law Office LLC", "555-0100", "office@example.test")
		e.fc.Lock(func() { e.m1.Status, e.m1.ClosedAt = "open", 0 })
		r = e.get("/client/home", e.tok(e.casey))
		want(t, r, http.StatusOK)
		lacks(t, r.Body, "Your portal access has ended")
		has(t, r.Body, "Dashboard")
		e.fc.Lock(func() {
			for _, m := range []*fakecore.Matter{e.m3, e.m4} {
				m.Status, m.ClosedAt = "open", 0
			}
		})
	})
}

var passwordRe = regexp.MustCompile(`<code data-temporary-password>([^<]+)</code>`)

func TestPortalAccess(t *testing.T) {
	e := newEnv(t)
	samM1 := e.tok(e.sam, e.m1.ID)

	t.Run("MOD-POR-09", func(t *testing.T) {
		pat := e.fc.AddContact("Pat Newclient", "pat@example.test")
		e.fc.AddParty(e.m1.ID, pat.ID, "client")
		r := e.postForm("/staff/access/"+pat.ID+"/grant", samM1, nil)
		want(t, r, http.StatusOK)
		has(t, r.Body, "Sent to pat@example.test")
		var linked *fakecore.User
		e.fc.Lock(func() {
			for _, u := range e.fc.Users {
				if u.LinkedContactID == pat.ID {
					linked = u
				}
			}
		})
		if linked == nil || strings.Join(linked.Roles, ",") != "client" || !linked.MustChangePassword {
			t.Fatalf("grant made no linked client user with a temporary password: %+v", linked)
		}
		quinn := e.fc.AddContact("Quinn Mailfail", "quinn@example.test")
		e.fc.AddParty(e.m1.ID, quinn.ID, "client")
		e.fc.AccessMailStatus = "failed"
		r = e.postForm("/staff/access/"+quinn.ID+"/grant", samM1, nil)
		want(t, r, http.StatusOK)
		has(t, r.Body, "Not sent", "/staff/access/"+quinn.ID+"/grant", "Send portal access")
		lacks(t, r.Body, "Sent to quinn@example.test")
		e.fc.AccessMailStatus = "sent"
	})

	t.Run("MOD-POR-10", func(t *testing.T) {
		r := e.postForm("/staff/access/"+e.cCasey.ID+"/temporary-password", samM1, nil)
		want(t, r, http.StatusOK)
		m := passwordRe.FindStringSubmatch(r.Body)
		if m == nil {
			t.Fatal("no temporary password shown")
		}
		again := e.get("/staff/access", samM1)
		want(t, again, http.StatusOK)
		lacks(t, again.Body, m[1], "data-temporary-password")
		next, err := e.fc.SignIn(e.casey.Email, m[1])
		if err != nil || next != "/portal/security" {
			t.Fatalf("sign-in with the temporary password: %q %v", next, err)
		}
		if r := e.get("/client/home", e.tok(e.casey)); r.Code == http.StatusOK {
			t.Error("the portal opened before the password change")
		}
		e.fc.ChangePassword(e.casey.ID, "chosen-"+m[1])
		want(t, e.get("/client/home", e.tok(e.casey)), http.StatusOK)
	})

	t.Run("MOD-POR-11", func(t *testing.T) {
		password := e.casey.Password
		contact := e.casey.LinkedContactID
		parties := len(e.fc.Parties)
		want(t, e.postForm("/staff/access/"+e.cCasey.ID+"/deactivate", samM1, nil), http.StatusOK)
		if _, err := e.fc.SignIn(e.casey.Email, password); err == nil {
			t.Error("Casey signed in with access off")
		}
		r := e.get("/staff/access", samM1)
		has(t, r.Body, "Portal off")
		want(t, e.postForm("/staff/access/"+e.cCasey.ID+"/reactivate", samM1, nil), http.StatusOK)
		if next, err := e.fc.SignIn(e.casey.Email, password); err != nil || next != "/portal" {
			t.Errorf("sign-in after turn on: %q %v", next, err)
		}
		if e.casey.LinkedContactID != contact || len(e.fc.Parties) != parties {
			t.Error("turning access off and on changed Casey's contact or matters")
		}
	})

	t.Run("MOD-POR-12", func(t *testing.T) {
		r := e.postForm("/staff/access/"+e.cDrew.ID+"/grant", samM1, nil)
		want(t, r, http.StatusBadRequest)
		has(t, r.Body, "That contact isn&#39;t a client on this matter.")
		want(t, e.postJSON("/api/access/"+e.cDrew.ID+"/grant", e.tok(e.sam), map[string]string{"matter_id": e.m1.ID}), http.StatusBadRequest)
		both := e.fc.AddContact("Morgan Both", "morgan@example.test")
		e.fc.AddParty(e.m1.ID, both.ID, "client")
		u := e.fc.AddUser("Morgan Both", "morgan@example.test", "staff", "client")
		e.fc.Lock(func() { u.LinkedContactID = both.ID })
		for _, action := range []string{"grant", "temporary-password", "deactivate"} {
			r := e.postJSON("/api/access/"+both.ID+"/"+action, e.tok(e.sam), map[string]string{"matter_id": e.m1.ID})
			want(t, r, http.StatusConflict)
		}
		if !u.Active || u.MustChangePassword {
			t.Error("the refused actions changed the staff user")
		}
		if r := e.get("/staff/access", e.tok(e.cal, e.m1.ID)); r.Code != http.StatusNotFound {
			t.Errorf("Cal's panel = %d, want 404", r.Code)
		}
		if r := e.get("/api/access?matter_id="+e.m1.ID, e.tok(e.cal)); r.Code != http.StatusNotFound {
			t.Errorf("Cal's access API = %d, want 404", r.Code)
		}
	})
}

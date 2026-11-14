package portal_test

import (
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu-client-portal/internal/dbtest"
	"github.com/CharitonMedia/togavsu-client-portal/internal/fakecore"
)

// Payments and signing through lawpay-docuseal's views, and the platform rules
// (SPEC section 8, MOD-POR-30 to 35).

var payHref = regexp.MustCompile(`href="(/portal/lawpay-docuseal/[^"]+)"`)

func providerLinks(body string) []*url.URL {
	var out []*url.URL
	for _, m := range payHref.FindAllStringSubmatch(body, -1) {
		u, err := url.Parse(html.UnescapeString(m[1]))
		if err == nil {
			out = append(out, u)
		}
	}
	return out
}

func TestPaymentsAndSigning(t *testing.T) {
	e := newEnv(t)
	e.setActive(e.casey, e.m1.ID)
	pages := []string{"/client/home", "/client/documents", "/client/invoices", "/client/invoices?invoice_id=" + e.i1.ID, "/client/account", "/client/messages"}

	t.Run("MOD-POR-30", func(t *testing.T) {
		rows := fakecore.Provider{
			InvoicePay: []map[string]any{{"invoice_id": e.i1.ID, "payable": true}},
			MatterPay:  []map[string]any{{"matter_id": e.m1.ID, "trust_deposits_enabled": true}},
			Signatures: []map[string]any{{"file_id": e.f1Shared.ID, "signing_request_id": "sr-1", "matter_id": e.m1.ID, "signer_contact_id": e.cCasey.ID, "status": "awaiting_client"}},
		}
		absent := rows
		declined := rows
		declined.Installed = true
		declined.Declined = map[string]bool{"invoice_payability": true, "matter_payability": true, "signature_requests": true}
		for name, p := range map[string]fakecore.Provider{"absent": absent, "declined": declined} {
			e.fc.Provider = p
			for _, path := range pages {
				r := e.get(path, e.tok(e.casey))
				if r.Code != http.StatusOK {
					t.Errorf("%s: %s = %d", name, path, r.Code)
					continue
				}
				if links := providerLinks(r.Body); len(links) != 0 {
					t.Errorf("%s: %s links to %v", name, path, links)
				}
				lacks(t, r.Body, ">Pay<", "Make a deposit", ">Sign<")
			}
		}
		e.fc.Provider = fakecore.Provider{}
	})

	t.Run("MOD-POR-31", func(t *testing.T) {
		e.fc.Provider = fakecore.Provider{Installed: true, InvoicePay: []map[string]any{{"invoice_id": e.i1.ID, "payable": true}}}
		r := e.get("/client/invoices", e.tok(e.casey))
		want(t, r, http.StatusOK)
		links := providerLinks(r.Body)
		if len(links) != 1 {
			t.Fatalf("pay links %v", links)
		}
		u := links[0]
		q := u.Query()
		if u.Path != "/portal/lawpay-docuseal/pay" || q.Get("purpose") != "invoice_pay" || q.Get("invoice_id") != e.i1.ID ||
			q.Get("return_to") != "/portal/client-portal/invoices?invoice_id="+e.i1.ID {
			t.Errorf("Pay opens %s", u)
		}
		if !strings.Contains(r.Body, `target="_top" href="/portal/lawpay-docuseal/pay?`) {
			t.Error("Pay doesn't open at the top level")
		}
		e.fc.Provider.InvoicePay = []map[string]any{{"invoice_id": e.i1.ID, "payable": false}}
		r = e.get("/client/invoices", e.tok(e.casey))
		if links := providerLinks(r.Body); len(links) != 0 {
			t.Errorf("Pay shows with payable false: %v", links)
		}
		e.fc.Provider.InvoicePay = []map[string]any{{"invoice_id": e.i1.ID, "payable": false, "unpayable_reason": "in_progress"}}
		r = e.get("/client/invoices", e.tok(e.casey))
		has(t, r.Body, "Payment processing")
		e.fc.Provider = fakecore.Provider{}
	})

	t.Run("MOD-POR-32", func(t *testing.T) {
		e.fc.Provider = fakecore.Provider{Installed: true, Signatures: []map[string]any{
			{"file_id": e.f1Shared.ID, "signing_request_id": "sr-casey", "matter_id": e.m1.ID, "signer_contact_id": e.cCasey.ID, "status": "awaiting_client"},
			{"file_id": e.f3.ID, "signing_request_id": "sr-jordan", "matter_id": e.m3.ID, "signer_contact_id": e.cJordan.ID, "status": "awaiting_client"},
		}}
		r := e.get("/client/documents", e.tok(e.casey))
		links := providerLinks(r.Body)
		if len(links) != 1 || links[0].Path != "/portal/lawpay-docuseal/sign" || links[0].Query().Get("signing_request_id") != "sr-casey" ||
			links[0].Query().Get("return_to") != "/portal/client-portal/documents" {
			t.Errorf("sign links %v", links)
		}
		home := e.get("/client/home", e.tok(e.casey))
		has(t, home.Body, "Waiting for your signature", "signing_request_id=sr-casey")
		e.setActive(e.casey, e.m3.ID)
		r = e.get("/client/documents", e.tok(e.casey))
		want(t, r, http.StatusOK)
		has(t, r.Body, e.f3.Name)
		if links := providerLinks(r.Body); len(links) != 0 {
			t.Errorf("Casey sees Jordan's signing request: %v", links)
		}
		lacks(t, r.Body, "sr-jordan", "data-signature")
		// Jordan sees their own.
		e.setActive(e.jordan, e.m3.ID)
		has(t, e.get("/client/documents", e.tok(e.jordan)).Body, "signing_request_id=sr-jordan")
		e.fc.Provider = fakecore.Provider{}
	})
}

func TestPlatform(t *testing.T) {
	e := newEnv(t)
	th, _ := e.startThread(e.sam, e.m1.ID, e.cCasey.ID, "Platform", "Hello.", e.f1Shared.ID)
	e.replyAs(e.casey, th.ID, "Unread one.")
	e.replyAs(e.casey, th.ID, "Unread two.")
	e.setActive(e.casey, e.m1.ID)
	want(t, e.get("/client/documents/"+e.f1Shared.ID+"/view", e.tok(e.casey)), http.StatusSeeOther)
	want(t, e.upload(e.tok(e.casey), e.m1.ID, "Upload.pdf", pdfBytes), http.StatusCreated)

	t.Run("MOD-POR-33", func(t *testing.T) {
		var schema, owner string
		err := e.db.Admin.QueryRow(`SELECT n.nspname, pg_get_userbyid(c.relowner) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.relname = 'message_threads'`).Scan(&schema, &owner)
		if err != nil || schema != dbtest.Schema || owner != dbtest.Role {
			t.Fatalf("message_threads is in %q owned by %q (%v)", schema, owner, err)
		}
		tables := e.dbCount(`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = $1`, dbtest.Schema)
		if tables != 9 { // eight tables and the thread_summary view
			t.Errorf("%d relations in %s, want 9", tables, dbtest.Schema)
		}
		var path string
		if err := e.db.Module.QueryRow(`SHOW search_path`).Scan(&path); err != nil || path != dbtest.Schema {
			t.Errorf("module search_path = %q (%v)", path, err)
		}
		for _, tbl := range dbtest.CoreTables {
			var id string
			err := e.db.Module.QueryRow(`SELECT id FROM core.` + tbl + ` LIMIT 1`).Scan(&id)
			if err == nil || !strings.Contains(err.Error(), "permission denied") {
				t.Errorf("module login read core.%s: %v", tbl, err)
			}
		}
		if _, err := e.db.Module.Exec(`CREATE TABLE core.intruder (id TEXT)`); err == nil {
			t.Error("module login created a table in core")
		}
	})

	t.Run("MOD-POR-34", func(t *testing.T) {
		for _, c := range []struct{ table, id string }{{"contacts", e.cCasey.ID}, {"matters", e.m1.ID}, {"users", e.sam.ID}, {"files", e.f1Shared.ID}} {
			_, err := e.db.Admin.Exec(`DELETE FROM core.`+c.table+` WHERE id = $1`, c.id)
			if err == nil || !strings.Contains(err.Error(), "violates foreign key constraint") {
				t.Errorf("hard delete of core.%s: %v", c.table, err)
			}
		}
		var schema string
		if err := e.db.Admin.QueryRow(`SELECT DISTINCT n.nspname FROM pg_constraint k JOIN pg_namespace n ON n.oid = k.connamespace
  WHERE k.contype = 'f' AND k.confrelid = 'core.contacts'::regclass`).Scan(&schema); err != nil || schema != dbtest.Schema {
			t.Errorf("keys onto core.contacts live in %q (%v)", schema, err)
		}
		if n := e.dbCount(`SELECT COUNT(*) FROM pg_constraint k JOIN pg_namespace n ON n.oid = k.connamespace
  WHERE n.nspname = $1 AND k.contype = 'f' AND k.confdeltype <> 'r'`, dbtest.Schema); n != 0 {
			t.Errorf("%d foreign keys are not ON DELETE RESTRICT", n)
		}
	})

	t.Run("MOD-POR-35", func(t *testing.T) {
		r := e.coreGet("/api/v1/matters/"+e.m1.ID+"?include=client-portal.thread_summary", e.tok(e.sam))
		want(t, r, http.StatusOK)
		var m struct {
			Included map[string][]struct {
				Active int `json:"active_threads"`
				Unread int `json:"unread_from_client"`
				Last   int `json:"last_message_at"`
			} `json:"included"`
		}
		r.json(t, &m)
		rows := m.Included["client-portal.thread_summary"]
		if len(rows) != 1 || rows[0].Active != 1 || rows[0].Unread != 2 || rows[0].Last == 0 {
			t.Errorf("thread_summary for Sam: %+v", rows)
		}
		for _, u := range []*fakecore.User{e.drew, e.jordan} {
			if r := e.coreGet("/api/v1/matters/"+e.m1.ID+"?include=client-portal.thread_summary", e.tok(u)); r.Code != http.StatusNotFound {
				t.Errorf("%s reading M1's summary = %d", u.Name, r.Code)
			}
		}
		if r := e.coreGet("/api/v1/matters/"+e.m4.ID+"?include=client-portal.thread_summary", e.tok(e.sam)); r.Code != http.StatusNotFound {
			t.Errorf("Sam reading M4's summary = %d", r.Code)
		}
	})
}

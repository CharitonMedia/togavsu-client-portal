package acceptance_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"html"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu-client-portal/internal/coremain"
	"github.com/CharitonMedia/togavsu/pkg/modulekit"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Payments and signing through lawpay-docuseal's views, and the platform rules
// (SPEC section 8, MOD-POR-30 to 35).

const provider = "lawpay-docuseal"

var payHref = regexp.MustCompile(`href="(/portal/lawpay-docuseal/[^"]+)"`)

func providerLinks(body string) []*url.URL {
	var out []*url.URL
	for _, m := range payHref.FindAllStringSubmatch(body, -1) {
		if u, err := url.Parse(html.UnescapeString(m[1])); err == nil {
			out = append(out, u)
		}
	}
	return out
}

// superDB opens the suite's database as the test server's superuser (the
// TOGAVSU_TEST_DATABASE_URL login coretest was given), for checks the core role can't
// make. It is closed at cleanup.
func (e *env) superDB() *sql.DB {
	e.t.Helper()
	su, err := url.Parse(os.Getenv("TOGAVSU_TEST_DATABASE_URL"))
	if err != nil {
		e.t.Fatal(err)
	}
	cu, err := url.Parse(e.c.DatabaseURL)
	if err != nil {
		e.t.Fatal(err)
	}
	su.Path = cu.Path
	d, err := sql.Open("pgx", su.String())
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = d.Close() })
	return d
}

// onlyModuleKeys runs stmt in a transaction with every core-owned foreign key trigger
// on table switched off, so only client-portal's keys onto it can refuse, and rolls
// the transaction back. It returns stmt's error.
func onlyModuleKeys(d *sql.DB, table, stmt string, args ...any) error {
	ctx := context.Background()
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT t.tgname FROM pg_trigger t
  JOIN pg_constraint k ON k.oid = t.tgconstraint JOIN pg_namespace n ON n.oid = k.connamespace
  WHERE t.tgrelid = $1::regclass AND k.contype = 'f' AND n.nspname <> 'mod_client_portal'`, table)
	if err != nil {
		return err
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		names = append(names, n)
	}
	rows.Close()
	for _, n := range names {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE `+table+` DISABLE TRIGGER "`+n+`"`); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, stmt, args...)
	return err
}

// installProvider installs the lawpay-docuseal fixture (testdata/lawpay-docuseal)
// signed with its own trusted key. It answers no pages; client-portal only reads its
// views and links to its tabs.
func (e *env) installProvider() *sql.DB {
	e.t.Helper()
	files, err := modulekit.ReadDir("testdata/lawpay-docuseal")
	if err != nil {
		e.t.Fatal(err)
	}
	coremain.Edit(files)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		e.t.Fatal(err)
	}
	signed, err := modulekit.Build(priv, files)
	if err != nil {
		e.t.Fatal(err)
	}
	e.c.Handle(provider, http.NotFoundHandler())
	e.c.TrustKey("Payments fixture key", pub)
	e.c.Install(signed)
	e.c.ApproveAll(provider)
	return e.c.ModuleDB(provider)
}

func TestPaymentsAndSigning(t *testing.T) {
	e := newEnv(t)
	pages := []string{"/client/home", "/client/documents", "/client/invoices", "/client/account", "/client/messages"}

	t.Run("MOD-POR-30", func(t *testing.T) {
		needs(t, clientReads, "CORE-10")
		e.setActive(casey, e.m1)
		for _, path := range pages {
			r := e.get(path, e.tok(casey))
			if r.Code != http.StatusOK {
				t.Errorf("%s with lawpay-docuseal absent = %d", path, r.Code)
				continue
			}
			if links := providerLinks(r.Body); len(links) != 0 {
				t.Errorf("%s links to %v", path, links)
			}
			lacks(t, r.Body, ">Pay<", "Make a deposit", ">Sign<")
		}
	})

	db := e.installProvider()
	inv := e.invoice(e.m1, "sent")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO invoice_payability_rows (invoice_id, payable) VALUES ($1, true)`, inv)
	exec(`INSERT INTO signature_requests_rows (signing_request_id, file_id, matter_id, signer_contact_id, status) VALUES ('sr-casey', $1, $2, $3, 'awaiting_client'), ('sr-jordan', $4, $5, $6, 'awaiting_client')`,
		e.f1Shared, e.m1, e.cCasey, e.f3, e.m3, e.cJordan)
	// The core serves the fixture's published view to client-portal, which was granted
	// its scope, as the viewer.
	if r := e.coreGet("/api/v1/modules/"+provider+"/views/signature_requests?record_id="+e.f1Shared, e.tok(sam)); r.Code != http.StatusOK || !strings.Contains(r.Body, "sr-casey") {
		t.Fatalf("signature_requests through the core for Sam: %d %s", r.Code, r.Body)
	}

	t.Run("MOD-POR-31", func(t *testing.T) {
		// Besides the CORE items: the core resolves a published view's invoice ids
		// through core.invoices, which billing (it keeps invoices in billing_records)
		// never fills, so invoice_payability reads nothing on the core main.
		needs(t, clientReads, "CORE-10", "CORE-09", "invoice views (core.invoices is empty; on no CORE item yet)")
		if r := e.coreGet("/api/v1/modules/"+provider+"/views/invoice_payability?record_id="+inv, e.tok(sam)); !strings.Contains(r.Body, inv) {
			t.Fatalf("invoice_payability through the core for Sam: %d %s", r.Code, r.Body)
		}
		e.setActive(casey, e.m1)
		r := e.get("/client/invoices", e.tok(casey))
		want(t, r, http.StatusOK)
		links := providerLinks(r.Body)
		if len(links) != 1 {
			t.Fatalf("pay links %v", links)
		}
		q := links[0].Query()
		if links[0].Path != "/portal/lawpay-docuseal/pay" || q.Get("purpose") != "invoice_pay" || q.Get("invoice_id") != inv {
			t.Errorf("Pay opens %s", links[0])
		}
		exec(`UPDATE invoice_payability_rows SET payable = false WHERE invoice_id = $1`, inv)
		if links := providerLinks(e.get("/client/invoices", e.tok(casey)).Body); len(links) != 0 {
			t.Errorf("Pay shows with payable false: %v", links)
		}
	})

	t.Run("MOD-POR-32", func(t *testing.T) {
		needs(t, clientReads, "CORE-10", "CORE-09")
		e.setActive(casey, e.m1)
		links := providerLinks(e.get("/client/documents", e.tok(casey)).Body)
		if len(links) != 1 || links[0].Path != "/portal/lawpay-docuseal/sign" || links[0].Query().Get("signing_request_id") != "sr-casey" {
			t.Errorf("sign links %v", links)
		}
		e.setActive(casey, e.m3)
		r := e.get("/client/documents", e.tok(casey))
		want(t, r, http.StatusOK)
		lacks(t, r.Body, "sr-jordan")
		e.setActive(jordan, e.m3)
		has(t, e.get("/client/documents", e.tok(jordan)).Body, "signing_request_id=sr-jordan")
	})
}

func TestPlatform(t *testing.T) {
	e := newEnv(t)
	th, _ := e.startThread(sam, e.m1, e.cCasey, "Platform", "Hello.", e.f1Shared)
	want(t, e.replyAs(avery, th.ID, "A second staff word."), http.StatusCreated)
	mdb := e.c.ModuleDB(ID)
	dep := e.c.Last(ID)

	t.Run("MOD-POR-33", func(t *testing.T) {
		if dep.Version != "1.0.0" || !e.c.SchemaExists(ID) {
			t.Fatalf("deployment %s %s, schema %v", dep.Module, dep.Version, e.c.SchemaExists(ID))
		}
		var schema, owner string
		err := mdb.QueryRow(`SELECT n.nspname, pg_get_userbyid(c.relowner) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.relname = 'message_threads'`).Scan(&schema, &owner)
		if err != nil || schema != "mod_client_portal" || owner != dep.Secrets["togavsu_db_role"] {
			t.Fatalf("message_threads is in %q owned by %q (%v)", schema, owner, err)
		}
		if n := e.c.Count(`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'mod_client_portal'`); n != 9 { // eight tables and the thread_summary view
			t.Errorf("%d relations in mod_client_portal, want 9", n)
		}
		for _, tbl := range []string{"contacts", "files", "matters", "users"} {
			var id string
			err := mdb.QueryRow(`SELECT id FROM core.` + tbl + ` LIMIT 1`).Scan(&id)
			if err == nil || !strings.Contains(err.Error(), "permission denied") {
				t.Errorf("module login read core.%s: %v", tbl, err)
			}
		}
		if _, err := mdb.Exec(`CREATE TABLE core.intruder (id TEXT)`); err == nil {
			t.Error("module login created a table in core")
		}
		// An update whose applied migration changed is stopped.
		changed := e.signed(func(f map[string][]byte) {
			f["togavsu-module.yaml"] = []byte(strings.Replace(string(f["togavsu-module.yaml"]), `version: "1.0.0"`, `version: "1.0.1"`, 1))
			f["migrations/0001_messages.sql"] = append(f["migrations/0001_messages.sql"], []byte("\n-- changed after it was applied\n")...)
		})
		r := e.c.TryUpdate(ID, changed)
		if r.Code != http.StatusUnprocessableEntity || !strings.Contains(r.Body, "migration") {
			t.Errorf("update with a changed applied migration = %d %s", r.Code, r.Body)
		}
		if e.c.Last(ID).Version != "1.0.0" {
			t.Error("the refused update was deployed")
		}
	})

	t.Run("MOD-POR-34", func(t *testing.T) {
		// Every key onto the core is ON DELETE RESTRICT.
		if n := e.c.Count(`SELECT COUNT(*) FROM pg_constraint k JOIN pg_namespace n ON n.oid = k.connamespace
  WHERE n.nspname = 'mod_client_portal' AND k.contype = 'f' AND k.confdeltype <> 'r'`); n != 0 {
			t.Errorf("%d foreign keys are not ON DELETE RESTRICT", n)
		}
		// On the real core the core's own keys refuse these deletes first, so each
		// delete runs with only the module's keys left armed (the core-owned key
		// triggers off, in a transaction that is rolled back): the module's key alone
		// refuses it and the refusal names its schema.
		admin := e.superDB()
		for _, c := range []struct{ table, id string }{{"contacts", e.cCasey}, {"matters", e.m1}, {"users", e.c.Staff.ID}, {"files", e.f1Shared}} {
			err := onlyModuleKeys(admin, "core."+c.table, `DELETE FROM core.`+c.table+` WHERE id = $1`, c.id)
			var pg *pgconn.PgError
			if !errors.As(err, &pg) || pg.Code != "23503" || pg.SchemaName != "mod_client_portal" {
				t.Errorf("hard delete of core.%s with only the module's keys: %v", c.table, err)
			}
		}
		if n := e.c.Count(`SELECT COUNT(*) FROM core.contacts WHERE id = $1`, e.cCasey); n != 1 {
			t.Error("the contact is gone")
		}
	})

	t.Run("MOD-POR-35", func(t *testing.T) {
		r := e.coreGet("/api/v1/matters/"+e.m1+"?include=client-portal.thread_summary", e.tok(sam))
		if r.Code != http.StatusOK {
			t.Fatalf("Sam reading M1 with the summary = %d %s", r.Code, r.Body)
		}
		var m struct {
			Included map[string][]struct {
				Active int   `json:"active_threads"`
				Unread int   `json:"unread_from_client"`
				Last   int64 `json:"last_message_at"`
			} `json:"included"`
		}
		// The counts are JSON numbers: the core passes the view's values through.
		if err := jsonInto(r.Body, &m); err != nil {
			t.Fatal(err)
		}
		rows := m.Included["client-portal.thread_summary"]
		if len(rows) != 1 || rows[0].Active != 1 || rows[0].Unread != 0 || rows[0].Last == 0 {
			t.Errorf("thread_summary for Sam: %+v (%s)", rows, r.Body)
		}
		for _, who := range []string{drew, jordan} {
			if r := e.coreGet("/api/v1/matters/"+e.m1+"?include=client-portal.thread_summary", e.tok(who)); r.Code != http.StatusNotFound && r.Code != http.StatusForbidden {
				t.Errorf("%s reading M1's summary = %d", who, r.Code)
			}
		}
		if r := e.coreGet("/api/v1/matters/"+e.m4+"?include=client-portal.thread_summary", e.tok(sam)); r.Code != http.StatusNotFound && r.Code != http.StatusForbidden {
			t.Errorf("Sam reading M4's summary = %d", r.Code)
		}
	})
}

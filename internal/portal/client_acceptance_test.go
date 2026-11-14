package portal_test

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu-client-portal/internal/fakecore"
)

// The client's portal, documents and uploads (SPEC section 8, MOD-POR-13 to 21).

func TestClientPortal(t *testing.T) {
	e := newEnv(t)

	t.Run("MOD-POR-13", func(t *testing.T) {
		drew := e.tok(e.drew)
		r := e.get("/client/documents", drew)
		want(t, r, http.StatusOK)
		has(t, r.Body, `data-matter="`+e.m2.ID+`"`, e.f2.Name)

		e.fc.Provider = fakecore.Provider{Installed: true,
			InvoicePay: []map[string]any{{"invoice_id": e.i1.ID, "payable": true}},
			MatterPay:  []map[string]any{{"matter_id": e.m1.ID, "trust_deposits_enabled": true}, {"matter_id": e.m3.ID, "trust_deposits_enabled": true}}}
		defer func() { e.fc.Provider = fakecore.Provider{} }()
		casey := e.tok(e.casey)
		docs := e.get("/client/documents", casey)
		want(t, docs, http.StatusOK)
		has(t, docs.Body, "/client/pick-matter?")
		lacks(t, docs.Body, `id="upload-form"`)
		msgs := e.get("/client/messages", casey)
		has(t, msgs.Body, "data-new-message", "/client/pick-matter?")
		invs := e.get("/client/invoices", casey)
		want(t, invs, http.StatusOK)
		has(t, invs.Body, e.i1.Number)
		lacks(t, invs.Body, "/portal/lawpay-docuseal/pay")
		if !strings.Contains(invs.Body, `class="button pay" href="/m/client-portal/client/pick-matter?`) {
			t.Error("Pay without a chosen matter doesn't open the picker")
		}
		home := e.get("/client/home", casey)
		lacks(t, home.Body, "/portal/lawpay-docuseal/pay")
		has(t, home.Body, "/client/pick-matter?")
		up := e.upload(casey, "", "a.pdf", []byte("%PDF-1.7"))
		want(t, up, http.StatusBadRequest)
		has(t, up.Body, "matter_required")
		if r := e.postJSON("/api/threads", casey, map[string]any{"body": "which matter?"}); r.Code != http.StatusBadRequest || !strings.Contains(r.Body, "matter_required") {
			t.Errorf("new message without a matter = %d %s", r.Code, r.Body)
		}

		r = e.postForm("/client/active-matter", casey, url.Values{"matter_id": {e.m1.ID}, "next": {"/client/documents"}})
		want(t, r, http.StatusSeeOther)
		if loc := r.Header.Get("Location"); !strings.HasPrefix(loc, "/m/client-portal/client/documents?") {
			t.Errorf("picker returns to %q", loc)
		}
		next := e.tok(e.casey) // the next sign-in
		docs = e.get("/client/documents", next)
		has(t, docs.Body, `data-matter="`+e.m1.ID+`"`)
		invs = e.get("/client/invoices", next)
		has(t, invs.Body, "/portal/lawpay-docuseal/pay?")
		home = e.get("/client/home", next)
		has(t, home.Body, "/portal/lawpay-docuseal/pay?", "trust_deposit", "matter_id="+e.m1.ID)
		has(t, e.get("/client/messages", next).Body, `name="matter_id" value="`+e.m1.ID+`"`)
	})

	t.Run("MOD-POR-14", func(t *testing.T) {
		e.setActive(e.casey, e.m1.ID)
		casey := e.tok(e.casey)
		has(t, e.get("/client/home", casey).Body, `data-new-docs="2"`)
		// A download alone, straight from the core, doesn't clear the dot.
		dl := e.coreGet("/api/v1/files/"+e.f1Shared.ID+"/versions/"+e.f1Shared.VersionID+"/download?disposition=attachment", casey)
		want(t, dl, http.StatusOK)
		has(t, e.get("/client/home", casey).Body, `data-new-docs="2"`)
		r := e.get("/client/documents/"+e.f1Shared.ID+"/view", casey)
		want(t, r, http.StatusSeeOther)
		if loc := r.Header.Get("Location"); loc != "/api/v1/files/"+e.f1Shared.ID+"/versions/"+e.f1Shared.VersionID+"/download?disposition=inline" {
			t.Errorf("view goes to %q", loc)
		}
		has(t, e.get("/client/home", casey).Body, `data-new-docs="1"`)
		want(t, e.get("/client/documents/"+e.f1Second.ID+"/view", casey), http.StatusSeeOther)
		home := e.get("/client/home", casey).Body
		lacks(t, home, "data-new-docs")
		has(t, home, "Nothing new")
		if r := e.get("/client/documents/"+e.f1Plain.ID+"/view", casey); r.Code != http.StatusNotFound {
			t.Errorf("view of an unshared file = %d", r.Code)
		}
	})

	t.Run("MOD-POR-15", func(t *testing.T) {
		e.setActive(e.casey, e.m1.ID)
		e.startThread(e.sam, e.m1.ID, e.cCasey.ID, "Hello", "Your inventory is ready.")
		e.fc.SetTrust(e.cCasey.ID, e.m1.ID, 50000)
		e.fc.Lock(func() {
			e.fc.Replenishments = append(e.fc.Replenishments, fakecore.Replenishment{ID: e.fc.NewID(), MatterID: e.m1.ID, ContactID: e.cCasey.ID,
				AmountCents: 150000, Message: "For the appraisal.", Status: "active"})
			e.fc.Appointments = append(e.fc.Appointments,
				fakecore.Appointment{Title: "Staff lunch", Kind: "internal", Start: "2026-11-05T12:00:00Z", MatterID: e.m1.ID},
				fakecore.Appointment{Title: "Signing meeting", Kind: "client_meeting", Start: "2026-11-09T16:00:00Z", Location: "Main office", MatterID: e.m1.ID},
				fakecore.Appointment{Title: "Hearing", Kind: "court_date", Start: "2026-12-01T15:00:00Z", MatterID: e.m1.ID})
		})
		home := e.get("/client/home", e.tok(e.casey))
		want(t, home, http.StatusOK)
		has(t, home.Body, `data-unread="1"`, `data-balance="100000"`, "$1000.00", `data-trust="50000"`, "$500.00",
			"data-replenishment=", "$1500.00", "For the appraisal.", `data-appointment="2026-11-09T16:00:00Z"`, "Signing meeting")
		lacks(t, home.Body, "Staff lunch")
	})

	t.Run("MOD-POR-16", func(t *testing.T) {
		casey := e.tok(e.casey)
		r := e.postForm("/client/account", casey, url.Values{"email": {"casey@example.test"}, "phone": {"555-0142"}, "city": {"Springfield"}})
		want(t, r, http.StatusOK)
		has(t, r.Body, `value="555-0142"`)
		var hist []fakecore.ContactInfo
		e.fc.Lock(func() { hist = append(hist, e.cCasey.History...) })
		if len(hist) != 2 || hist[0].Phone != "555-0199" || hist[0].EffectiveTo == 0 || hist[1].Phone != "555-0142" || hist[1].EffectiveTo != 0 {
			t.Fatalf("contact history = %+v", hist)
		}
		found := false
		e.fc.Lock(func() {
			for _, ev := range e.fc.Events {
				if ev.Action == "contact.updated" && ev.ActorID == e.casey.ID && ev.ActorKind == "client" {
					found = true
				}
			}
		})
		if !found {
			t.Error("no timeline row with Casey as the actor")
		}
		bad := e.postForm("/client/account", casey, url.Values{"email": {"not-an-address"}})
		want(t, bad, http.StatusBadRequest)
	})
}

// Synthetic file contents for each allowed type.
var (
	pdfBytes  = []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\n%%EOF\n")
	jpegBytes = []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00synthetic")
	heicBytes = []byte("\x00\x00\x00\x18ftypheic\x00\x00\x00\x00mif1heic")
	docxBytes = []byte("PK\x03\x04\x14\x00\x00\x00[Content_Types].xml word/document.xml synthetic")
	xlsxBytes = []byte("PK\x03\x04\x14\x00\x00\x00[Content_Types].xml xl/workbook.xml synthetic")
	exeBytes  = []byte("MZ\x90\x00\x03\x00\x00\x00\x04\x00synthetic")
)

func TestClientUploads(t *testing.T) {
	e := newEnv(t)
	casey := func() string { return e.tok(e.casey) }

	t.Run("MOD-POR-17", func(t *testing.T) {
		names := []string{"Scan.pdf", "Photo.jpg", "IMG_0042.HEIC", "Letter.docx", "Budget.xlsx"}
		data := [][]byte{pdfBytes, jpegBytes, heicBytes, docxBytes, xlsxBytes}
		for i, name := range names {
			r := e.upload(casey(), e.m1.ID, name, data[i])
			want(t, r, http.StatusCreated)
		}
		// An agent's chunked upload through the JSON endpoints, in small chunks.
		e.fc.ChunkSize = 16
		cr := e.postJSON("/api/uploads", casey(), map[string]any{"matter_id": e.m1.ID, "file_name": "Agent notes.pdf", "size_bytes": len(pdfBytes)})
		want(t, cr, http.StatusCreated)
		var up struct {
			ID    string `json:"id"`
			Chunk int    `json:"chunk_size_bytes"`
		}
		cr.json(t, &up)
		for off := 0; off < len(pdfBytes); off += up.Chunk {
			end := min(off+up.Chunk, len(pdfBytes))
			rng := "bytes " + strconv.Itoa(off) + "-" + strconv.Itoa(end-1) + "/" + strconv.Itoa(len(pdfBytes))
			want(t, e.putChunk("/api/uploads/"+up.ID, casey(), pdfBytes[off:end], rng), http.StatusOK)
		}
		want(t, e.postJSON("/api/uploads/"+up.ID+"/complete", casey(), map[string]any{}), http.StatusCreated)
		// The page upload relays in the core's chunk size too.
		want(t, e.upload(casey(), e.m1.ID, "Small chunks.pdf", pdfBytes), http.StatusCreated)
		e.fc.ChunkSize = 8 << 20
		names = append(names, "Agent notes.pdf", "Small chunks.pdf")
		staff := e.coreGet("/api/v1/files?matter_id="+e.m1.ID, e.tok(e.sam))
		want(t, staff, http.StatusOK)
		var page struct {
			Data []struct {
				Name, Source, Origin string
				MatterID             string `json:"matter_id"`
				Shared               bool   `json:"shared_with_client"`
			} `json:"data"`
		}
		staff.json(t, &page)
		got := map[string]int{}
		for _, f := range page.Data {
			if f.Source == "client_upload" {
				if f.Origin != "received" || !f.Shared || f.MatterID != e.m1.ID {
					t.Errorf("%s: origin %q shared %v matter %s", f.Name, f.Origin, f.Shared, f.MatterID)
				}
				got[f.Name]++
			}
		}
		for _, n := range names {
			if got[n] != 1 {
				t.Errorf("%s landed %d times", n, got[n])
			}
		}
		if got["Budget.xlsx"] != 1 {
			t.Error("the xlsx didn't keep its .xlsx name")
		}
		if n := e.dbCount(`SELECT COUNT(*) FROM mod_client_portal.client_uploads WHERE matter_id = $1`, e.m1.ID); n != 7 {
			t.Errorf("upload ledger has %d rows", n)
		}
		notified := 0
		e.fc.Lock(func() {
			for _, n := range e.fc.Notifications {
				if n.Kind == "client-portal.client_upload" {
					notified++
				}
			}
		})
		if notified == 0 {
			t.Error("the matter team got no upload notification")
		}
	})

	t.Run("MOD-POR-18", func(t *testing.T) {
		before := e.fc.FileCount()
		r := e.upload(casey(), e.m1.ID, "invoice.pdf", exeBytes)
		want(t, r, http.StatusBadRequest)
		has(t, r.Body, "That file has to be a PDF, Word, Excel, text, or image file.")
		r = e.upload(casey(), e.m1.ID, "empty.pdf", nil)
		want(t, r, http.StatusBadRequest)
		has(t, r.Body, "That file was empty.")
		// The chunked endpoint refuses the same way.
		cr := e.postJSON("/api/uploads", casey(), map[string]any{"matter_id": e.m1.ID, "file_name": "setup.pdf", "size_bytes": len(exeBytes)})
		want(t, cr, http.StatusCreated)
		var up struct {
			ID string `json:"id"`
		}
		cr.json(t, &up)
		chunk := e.putChunk("/api/uploads/"+up.ID, casey(), exeBytes, "bytes 0-"+strconv.Itoa(len(exeBytes)-1)+"/"+strconv.Itoa(len(exeBytes)))
		if chunk.Code != http.StatusBadRequest || !strings.Contains(chunk.Body, "That file has to be") {
			t.Errorf("chunked exe = %d %s", chunk.Code, chunk.Body)
		}
		if r := e.postJSON("/api/uploads", casey(), map[string]any{"matter_id": e.m1.ID, "file_name": "empty.pdf", "size_bytes": 0}); r.Code != http.StatusBadRequest {
			t.Errorf("chunked empty = %d", r.Code)
		}
		if e.fc.FileCount() != before {
			t.Error("a refused upload left a file record")
		}
	})

	t.Run("MOD-POR-19", func(t *testing.T) {
		before := e.fc.FileCount()
		q := url.Values{"name": {"huge.pdf"}, "size": {strconv.Itoa(251 << 20)}, "matter_id": {e.m1.ID}}
		r := e.do(http.MethodPost, "/client/upload?"+q.Encode(), casey(), bytes.NewReader(pdfBytes), "application/octet-stream")
		want(t, r, http.StatusRequestEntityTooLarge)

		// The 24-hour caps per matter: 100 files, then 2 GB. Earlier uploads are seeded
		// into the ledger, each backed by a core file id.
		seed := func(n int, size int64) {
			for i := 0; i < n; i++ {
				id := e.fc.NewID()
				if _, err := e.db.Admin.Exec(`INSERT INTO core.files (id) VALUES ($1)`, id); err != nil {
					t.Fatal(err)
				}
				if _, err := e.db.Admin.Exec(`INSERT INTO mod_client_portal.client_uploads (file_id, user_id, matter_id, size_bytes, uploaded_at) VALUES ($1, $2, $3, $4, $5)`,
					id, e.casey.ID, e.m3.ID, size, e.now.Add(-time.Hour).Unix()); err != nil {
					t.Fatal(err)
				}
			}
		}
		clear := func() {
			if _, err := e.db.Admin.Exec(`DELETE FROM mod_client_portal.client_uploads WHERE matter_id = $1`, e.m3.ID); err != nil {
				t.Fatal(err)
			}
		}
		daily := "That's as much as this portal accepts in a day. Call the office and we'll take the rest another way."
		seed(99, 1)
		want(t, e.upload(casey(), e.m3.ID, "ninety-nine.pdf", pdfBytes), http.StatusCreated)
		r = e.upload(casey(), e.m3.ID, "one-hundred-one.pdf", pdfBytes)
		want(t, r, http.StatusRequestEntityTooLarge)
		has(t, r.Body, strings.ReplaceAll(daily, "'", `'`))
		clear()
		seed(1, 2048<<20-int64(len(pdfBytes))+1)
		r = e.upload(casey(), e.m3.ID, "over.pdf", pdfBytes)
		want(t, r, http.StatusRequestEntityTooLarge)
		has(t, r.Body, "payload_too_large")
		clear()
		// Outside the 24 hours the caps reset.
		if _, err := e.db.Admin.Exec(`UPDATE mod_client_portal.client_uploads SET uploaded_at = $1`, e.now.Add(-25*time.Hour).Unix()); err != nil {
			t.Fatal(err)
		}
		before = e.fc.FileCount()

		// The rate window: 90 uploads in 5 minutes pass, the 91st answers 429.
		e.now = e.now.Add(10 * time.Minute)
		if _, err := e.db.Admin.Exec(`DELETE FROM mod_client_portal.upload_windows`); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 90; i++ {
			if r := e.upload(casey(), e.m4.ID, "page-"+strconv.Itoa(i)+".pdf", pdfBytes); r.Code != http.StatusCreated {
				t.Fatalf("upload %d = %d %s", i+1, r.Code, r.Body)
			}
		}
		e.now = e.now.Add(4 * time.Minute)
		count := e.fc.FileCount()
		r = e.upload(casey(), e.m4.ID, "page-91.pdf", pdfBytes)
		want(t, r, http.StatusTooManyRequests)
		if ra, err := strconv.Atoi(r.Header.Get("Retry-After")); err != nil || ra <= 0 || ra > 60 {
			t.Errorf("Retry-After = %q", r.Header.Get("Retry-After"))
		}
		if e.fc.FileCount() != count || count != before+90 {
			t.Errorf("file records: %d before, %d after the window, %d now", before, count, e.fc.FileCount())
		}
		e.now = e.now.Add(time.Minute)
		want(t, e.upload(casey(), e.m4.ID, "page-91.pdf", pdfBytes), http.StatusCreated)
	})

	t.Run("MOD-POR-20", func(t *testing.T) {
		e.now = e.now.Add(10 * time.Minute)
		batch := []struct {
			name string
			data []byte
		}{{"a.pdf", pdfBytes}, {"b.jpg", jpegBytes}, {"virus.pdf", exeBytes}, {"c.docx", docxBytes}, {"d.xlsx", xlsxBytes}}
		landed := 0
		var failed []string
		for _, f := range batch {
			r := e.upload(casey(), e.m1.ID, f.name, f.data)
			switch r.Code {
			case http.StatusCreated:
				landed++
			default:
				failed = append(failed, f.name)
			}
		}
		if landed != 4 || len(failed) != 1 || failed[0] != "virus.pdf" {
			t.Errorf("landed %d, failed %v", landed, failed)
		}
		// The page script sends each file alone and keeps every result listed.
		js := e.get("/static/portal.js", "")
		has(t, js.Body, "upload-results", `"failed"`)
	})

	t.Run("MOD-POR-21", func(t *testing.T) {
		r := e.postJSON("/api/archives", casey(), map[string]string{"matter_id": e.m1.ID})
		want(t, r, http.StatusOK)
		zr, err := zip.NewReader(bytes.NewReader([]byte(r.Body)), int64(len(r.Body)))
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, f := range zr.File {
			rc, _ := f.Open()
			_, _ = io.Copy(io.Discard, rc)
			rc.Close()
			got = append(got, strings.SplitN(f.Name, "-", 6)[5])
		}
		docs := e.coreGet("/api/v1/files?matter_id="+e.m1.ID, casey())
		var page struct {
			Data []struct{ Name string } `json:"data"`
		}
		docs.json(t, &page)
		var visible []string
		for _, f := range page.Data {
			visible = append(visible, f.Name)
		}
		sort.Strings(got)
		sort.Strings(visible)
		if strings.Join(got, "|") != strings.Join(visible, "|") {
			t.Errorf("zip holds %v, Casey sees %v", got, visible)
		}
		for _, n := range got {
			if n == e.f1Plain.Name || n == e.f1Atty.Name {
				t.Errorf("zip holds %s", n)
			}
		}
	})
}

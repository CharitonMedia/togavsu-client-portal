package acceptance_test

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The client's portal, documents and uploads (SPEC section 8, MOD-POR-13 to 21).

func TestClientPortal(t *testing.T) {
	e := newEnv(t)

	t.Run("MOD-POR-13", func(t *testing.T) {
		needs(t, clientReads, "CORE-10")
		r := e.get("/client/documents", e.tok(drew))
		want(t, r, http.StatusOK)
		has(t, r.Body, `data-matter="`+e.m2+`"`, "Drew deed.pdf")

		tok := e.tok(casey)
		docs := e.get("/client/documents", tok)
		want(t, docs, http.StatusOK)
		has(t, docs.Body, "/client/pick-matter?")
		lacks(t, docs.Body, `id="upload-form"`)
		has(t, e.get("/client/messages", tok).Body, "data-new-message", "/client/pick-matter?")
		up := e.upload(tok, "", "a.pdf", pdfBytes)
		want(t, up, http.StatusBadRequest)
		has(t, up.Body, "matter_required")
		if r := e.postJSON("/api/threads", tok, map[string]any{"body": "which matter?"}); r.Code != http.StatusBadRequest || !strings.Contains(r.Body, "matter_required") {
			t.Errorf("new message without a matter = %d %s", r.Code, r.Body)
		}
		r = e.postForm("/client/active-matter", tok, url.Values{"matter_id": {e.m1}, "next": {"/client/documents"}})
		want(t, r, http.StatusSeeOther)
		if loc := r.Header.Get("Location"); !strings.HasPrefix(loc, "/m/client-portal/client/documents?") {
			t.Errorf("picker returns to %q", loc)
		}
		next := e.tok(casey) // the next sign-in
		has(t, e.get("/client/documents", next).Body, `data-matter="`+e.m1+`"`)
		has(t, e.get("/client/messages", next).Body, `name="matter_id" value="`+e.m1+`"`)
	})

	t.Run("MOD-POR-14", func(t *testing.T) {
		needs(t, clientReads)
		second := e.file(e.m1, "Inventory.pdf", true, false)
		e.setActive(casey, e.m1)
		tok := e.tok(casey)
		has(t, e.get("/client/home", tok).Body, `data-new-docs="2"`)
		version, _ := e.c.Admin.Must(http.StatusOK, "GET", "/api/v1/files/"+e.f1Shared, nil).JSON["current_version_id"].(string)
		// A download alone, straight from the core, doesn't clear the dot.
		if r := e.coreGet("/api/v1/files/"+e.f1Shared+"/versions/"+version+"/download?disposition=attachment", tok); r.Code != http.StatusOK {
			t.Fatalf("download = %d", r.Code)
		}
		has(t, e.get("/client/home", tok).Body, `data-new-docs="2"`)
		want(t, e.get("/client/documents/"+e.f1Shared+"/view", tok), http.StatusSeeOther)
		has(t, e.get("/client/home", tok).Body, `data-new-docs="1"`)
		want(t, e.get("/client/documents/"+second+"/view", tok), http.StatusSeeOther)
		home := e.get("/client/home", tok).Body
		lacks(t, home, "data-new-docs")
		has(t, home, "Nothing new")
		if r := e.get("/client/documents/"+e.f1Plain+"/view", tok); r.Code != http.StatusNotFound {
			t.Errorf("view of an unshared file = %d", r.Code)
		}
	})

	t.Run("MOD-POR-15", func(t *testing.T) {
		needs(t, clientReads, "CORE-12", "CORE-15")
		e.setActive(casey, e.m1)
		e.startThread(sam, e.m1, e.cCasey, "Hello", "Your inventory is ready.")
		sent := e.invoice(e.m1, "sent")
		e.c.Attorney.Must(http.StatusCreated, "POST", "/api/v1/calendar-events", map[string]any{"matter_id": e.m1, "title": "Signing meeting",
			"kind": "client_meeting", "start": time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339), "end": time.Now().Add(73 * time.Hour).UTC().Format(time.RFC3339)})
		home := e.get("/client/home", e.tok(casey))
		want(t, home, http.StatusOK)
		has(t, home.Body, `data-unread="1"`, "data-balance=", "data-trust=", "Signing meeting", e.invoiceNumber(sent))
	})

	t.Run("MOD-POR-16", func(t *testing.T) {
		needs(t, clientReads, "CORE-13")
		tok := e.tok(casey)
		r := e.postForm("/client/account", tok, url.Values{"email": {"cleo@client.example"}, "phone": {"555-0142"}, "city": {"Springfield"}})
		want(t, r, http.StatusOK)
		has(t, r.Body, `value="555-0142"`)
		phones := e.c.Admin.Must(http.StatusOK, "GET", "/api/v1/contact-phones?contact_id="+e.cCasey, nil)
		if !strings.Contains(phones.Body, "effective_to") {
			t.Errorf("the old number kept no end date: %s", phones.Body)
		}
		if n := e.c.Count(`SELECT COUNT(*) FROM core.events WHERE action = 'contact.updated' AND actor_user_id = $1`, e.c.Client.ID); n == 0 {
			t.Error("no timeline row with Casey as the actor")
		}
		want(t, e.postForm("/client/account", tok, url.Values{"email": {"not-an-address"}}), http.StatusBadRequest)
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

type fileRow struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Source   string `json:"source"`
	Origin   string `json:"origin"`
	MatterID string `json:"matter_id"`
	Shared   bool   `json:"shared_with_client"`
}

// files lists matter's files as the admin.
func (e *env) files(matter string) []fileRow {
	e.t.Helper()
	r := e.c.Admin.Must(http.StatusOK, "GET", "/api/v1/files?limit=200&matter_id="+matter, nil)
	var page struct {
		Data []fileRow `json:"data"`
	}
	if err := jsonInto(r.Body, &page); err != nil {
		e.t.Fatal(err)
	}
	return page.Data
}

func TestClientUploads(t *testing.T) {
	e := newEnv(t)
	tok := func() string { return e.tok(casey) }

	t.Run("MOD-POR-17", func(t *testing.T) {
		needs(t, clientReads, "CORE-16")
		names := []string{"Scan.pdf", "Photo.jpg", "IMG_0042.HEIC", "Letter.docx", "Budget.xlsx"}
		data := [][]byte{pdfBytes, jpegBytes, heicBytes, docxBytes, xlsxBytes}
		for i, name := range names {
			want(t, e.upload(tok(), e.m1, name, data[i]), http.StatusCreated)
		}
		got := map[string]int{}
		for _, f := range e.files(e.m1) {
			if f.Source == "client_upload" {
				if f.Origin != "received" || !f.Shared || f.MatterID != e.m1 {
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
		if r := e.c.Tab(sam, ID, "inbox", ""); r.Code != http.StatusOK {
			t.Errorf("staff inbox after uploads = %d", r.Code)
		}
	})

	t.Run("MOD-POR-18", func(t *testing.T) {
		needs(t, clientReads)
		before := len(e.files(e.m1))
		r := e.upload(tok(), e.m1, "invoice.pdf", exeBytes)
		want(t, r, http.StatusBadRequest)
		has(t, r.Body, "That file has to be a PDF, Word, Excel, text, or image file.")
		r = e.upload(tok(), e.m1, "empty.pdf", nil)
		want(t, r, http.StatusBadRequest)
		has(t, r.Body, "That file was empty.")
		if len(e.files(e.m1)) != before {
			t.Error("a refused upload left a file record")
		}
	})

	t.Run("MOD-POR-19", func(t *testing.T) {
		needs(t, clientReads)
		before := len(e.files(e.m3))
		q := url.Values{"name": {"huge.pdf"}, "size": {strconv.Itoa(251 << 20)}, "matter_id": {e.m3}}
		want(t, e.do(http.MethodPost, "/client/upload?"+q.Encode(), tok(), pdfBytes, "application/octet-stream"), http.StatusRequestEntityTooLarge)
		// The per-matter daily caps, lowered through the module's settings.
		e.settings(map[string]string{"upload_daily_files": "2"})
		want(t, e.upload(tok(), e.m3, "one.pdf", pdfBytes), http.StatusCreated)
		want(t, e.upload(tok(), e.m3, "two.pdf", pdfBytes), http.StatusCreated)
		r := e.upload(tok(), e.m3, "three.pdf", pdfBytes)
		want(t, r, http.StatusRequestEntityTooLarge)
		has(t, r.Body, "That&#39;s as much as this portal accepts in a day.")
		e.settings(map[string]string{"upload_daily_files": "100", "upload_rate_per_5min": "3"})
		e.advance(10 * time.Minute)
		for i := 0; i < 3; i++ {
			want(t, e.upload(tok(), e.m4, "page-"+strconv.Itoa(i)+".pdf", pdfBytes), http.StatusCreated)
		}
		r = e.upload(tok(), e.m4, "page-4.pdf", pdfBytes)
		want(t, r, http.StatusTooManyRequests)
		if ra, err := strconv.Atoi(r.Header.Get("Retry-After")); err != nil || ra <= 0 {
			t.Errorf("Retry-After = %q", r.Header.Get("Retry-After"))
		}
		if n := len(e.files(e.m3)); n != before+2 {
			t.Errorf("M3 has %d new file records, want 2", n-before)
		}
	})

	t.Run("MOD-POR-20", func(t *testing.T) {
		needs(t, clientReads)
		e.advance(10 * time.Minute)
		landed := 0
		var failed []string
		for _, f := range []struct {
			name string
			data []byte
		}{{"a.pdf", pdfBytes}, {"b.jpg", jpegBytes}, {"virus.pdf", exeBytes}, {"c.docx", docxBytes}, {"d.xlsx", xlsxBytes}} {
			if r := e.upload(tok(), e.m1, f.name, f.data); r.Code == http.StatusCreated {
				landed++
			} else {
				failed = append(failed, f.name)
			}
		}
		if landed != 4 || len(failed) != 1 || failed[0] != "virus.pdf" {
			t.Errorf("landed %d, failed %v", landed, failed)
		}
		has(t, e.get("/static/portal.js", "").Body, "upload-results", `"failed"`)
	})

	t.Run("MOD-POR-21", func(t *testing.T) {
		needs(t, clientReads, "CORE-09")
		r := e.postJSON("/api/archives", tok(), map[string]string{"matter_id": e.m1})
		want(t, r, http.StatusOK)
		zr, err := zip.NewReader(bytes.NewReader([]byte(r.Body)), int64(len(r.Body)))
		if err != nil {
			t.Fatal(err)
		}
		var got, visible []string
		for _, f := range zr.File {
			got = append(got, f.Name[strings.LastIndex(f.Name, "-")+1:])
		}
		var page struct {
			Data []fileRow `json:"data"`
		}
		if err := jsonInto(e.coreGet("/api/v1/files?matter_id="+e.m1, tok()).Body, &page); err != nil {
			t.Fatal(err)
		}
		for _, f := range page.Data {
			visible = append(visible, f.Name)
		}
		sort.Strings(got)
		sort.Strings(visible)
		if strings.Join(got, "|") != strings.Join(visible, "|") {
			t.Errorf("zip holds %v, Casey sees %v", got, visible)
		}
	})
}

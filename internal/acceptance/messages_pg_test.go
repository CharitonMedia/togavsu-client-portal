package acceptance_test

import (
	"net/http"
	"net/url"
	"testing"
)

// Messages (SPEC section 8, MOD-POR-22 to 29).

// shared reads a file's shared_with_client flag as the admin.
func (e *env) shared(file string) bool {
	e.t.Helper()
	v, _ := e.c.Admin.Must(http.StatusOK, "GET", "/api/v1/files/"+file, nil).JSON["shared_with_client"].(bool)
	return v
}

// notificationStatus is the module's mail notice row for a message.
func (e *env) notificationStatus(message string) string {
	e.t.Helper()
	var s string
	if err := e.c.ModuleDB(ID).QueryRow(`SELECT status FROM message_notifications WHERE message_id = $1`, message).Scan(&s); err != nil {
		e.t.Fatal(err)
	}
	return s
}

func TestMessages(t *testing.T) {
	e := newEnv(t)

	t.Run("MOD-POR-22", func(t *testing.T) {
		th, msg := e.startThread(sam, e.m1, e.cCasey, "Welcome", "Welcome to the portal.")
		if th.MatterID != e.m1 || th.ContactID != e.cCasey || th.Status != "active" || msg.AuthorKind != "staff" {
			t.Fatalf("thread %+v message %+v", th, msg)
		}
		// The same through the matter tab's form.
		r := e.postForm("/staff/threads", e.tok(sam, e.m1), url.Values{"matter_id": {e.m1}, "contact_id": {e.cCasey}, "subject": {"Form"}, "body": {"From the form."}})
		want(t, r, http.StatusOK)
		has(t, r.Body, "From the form.")
		// The matter's Messages tab through the core's frame lists both.
		tab := e.c.Tab(sam, ID, "messages", e.m1)
		if tab.Code != http.StatusOK {
			t.Fatalf("Messages tab through the core = %d %s", tab.Code, tab.Body)
		}
		has(t, tab.Body, "Welcome", "Form")

		closed, cClosed := e.c.Matter("Closed Matter", "", nil)
		e.c.Admin.Must(http.StatusOK, "POST", "/api/v1/matters/"+closed+"/close", map[string]any{"reason": "Synthetic close"})
		prospect, cProspect := e.c.Matter("Prospect Matter", "", map[string]any{"status": "prospect"})
		n := e.messageCount()
		for _, c := range []struct{ name, matter, contact string }{
			{"closed matter", closed, cClosed}, {"prospect matter", prospect, cProspect},
			{"Drew on M1", e.m1, e.cDrew}, {"a guessed contact", e.m1, guessedID},
		} {
			r := e.postJSON("/api/threads", e.tok(sam), map[string]any{"matter_id": c.matter, "contact_id": c.contact, "body": "Hello"})
			if r.Code != http.StatusBadRequest {
				t.Errorf("thread on %s = %d, want 400: %s", c.name, r.Code, r.Body)
			}
		}
		if e.messageCount() != n {
			t.Error("a refused thread wrote a message")
		}
	})

	t.Run("MOD-POR-23", func(t *testing.T) {
		th, _ := e.startThread(sam, e.m1, e.cCasey, "Draft", "Here is the draft.", e.f1Plain)
		if !e.shared(e.f1Plain) {
			t.Fatal("the attached file didn't become shared")
		}
		if n := e.events(e.m1, "client_portal.file_sent_to_client"); n != 1 {
			t.Fatalf("%d sent-to-client rows, want 1", n)
		}
		want(t, e.replyAs(sam, th.ID, "Again, the draft.", e.f1Plain), http.StatusCreated)
		if n := e.events(e.m1, "client_portal.file_sent_to_client"); n != 2 {
			t.Fatalf("%d sent-to-client rows after attaching again, want 2", n)
		}
		n, rows := e.messageCount(), e.events(e.m1, "client_portal.file_sent_to_client")
		for _, f := range []string{e.f2, e.f1Atty} {
			r := e.replyAs(sam, th.ID, "Wrong file.", f)
			want(t, r, http.StatusBadRequest)
			has(t, r.Body, "A document you attached can't be sent.")
			r = e.postJSON("/api/threads", e.tok(sam), map[string]any{"matter_id": e.m1, "contact_id": e.cCasey, "body": "Wrong file.", "file_ids": []string{f}})
			want(t, r, http.StatusBadRequest)
		}
		if e.messageCount() != n || e.events(e.m1, "client_portal.file_sent_to_client") != rows {
			t.Error("a refused attachment wrote something")
		}

		needs(t, clientReads)
		_, ms := e.openAs(casey, th.ID)
		if len(ms) != 2 || len(ms[0].Attachments) != 1 || ms[0].Attachments[0].FileID != e.f1Plain {
			t.Errorf("Casey's view of the attachment: %+v", ms)
		}
	})

	t.Run("MOD-POR-24", func(t *testing.T) {
		// With email_client_on_staff_message off, no mail is asked for.
		e.settings(map[string]string{"email_client_on_staff_message": "false"})
		th, quiet := e.startThread(sam, e.m1, e.cCasey, "Quiet", "No mail for this one.")
		if s := e.notificationStatus(quiet.ID); s != "skipped" {
			t.Errorf("notification row %q, want skipped", s)
		}
		e.settings(map[string]string{"email_client_on_staff_message": "true"})

		needs(t, "CORE-04", "CORE-05")
		r := e.replyAs(sam, th.ID, "The settlement figure is 4815162342.")
		want(t, r, http.StatusCreated)
		var reply struct {
			ID string `json:"id"`
		}
		r.json(t, &reply)
		if s := e.notificationStatus(reply.ID); s != "sent" {
			t.Errorf("notification row %q, want sent", s)
		}
		if n := e.c.Count(`SELECT COUNT(*) FROM core.events WHERE matter_id = $1 AND action = 'mail.sent' AND summary LIKE '%4815162342%'`, e.m1); n != 0 {
			t.Error("the mail's timeline row carries the message body")
		}
	})

	t.Run("MOD-POR-25", func(t *testing.T) {
		needs(t, clientReads, "CORE-01", "CORE-03")
		e.c.Admin.Must(http.StatusOK, "PATCH", "/api/v1/matters/"+e.m1, map[string]any{"assigned_staff_user_ids": []string{e.c.Staff.ID}})
		e.setActive(casey, e.m1)
		r := e.postForm("/client/messages", e.tok(casey), url.Values{"subject": {"Question"}, "body": {"My secret word is periwinkle."}})
		want(t, r, http.StatusOK)
		for _, who := range []string{avery, sam} {
			list := e.c.As(who).Must(http.StatusOK, "GET", "/api/v1/notifications", nil)
			has(t, list.Body, "client-portal.client_message")
			lacks(t, list.Body, "periwinkle")
		}
		if e.events(e.m1, "client_portal.message_sent") == 0 {
			t.Error("no message_sent timeline row for the client message")
		}
	})

	t.Run("MOD-POR-26", func(t *testing.T) {
		needs(t, clientReads, "CORE-19", "CORE-08")
		th, msg := e.startThread(sam, e.m1, e.cCasey, "Receipt", "Please confirm.")
		_, ms := e.openAs(casey, th.ID)
		if ms[0].ID != msg.ID || ms[0].ReadAt == nil {
			t.Fatalf("Sam's message not read after Casey opened it: %+v", ms[0])
		}
		e.openAs(casey, th.ID)
		if n := e.events(e.m1, "client_portal.message_viewed"); n != 1 {
			t.Fatalf("%d viewed rows, want 1", n)
		}
		base := e.badge(sam)
		want(t, e.replyAs(casey, th.ID, "Confirmed."), http.StatusCreated)
		if e.badge(sam) != base+1 {
			t.Fatal("Sam's badge didn't rise")
		}
		page := e.get("/staff/thread/"+th.ID, e.tok(sam))
		want(t, page, http.StatusOK)
		has(t, page.Body, "Confirmed.", e.c.Staff.Name)
		if e.badge(sam) != base {
			t.Error("Sam opening the thread didn't lower the badge")
		}
	})

	t.Run("MOD-POR-27", func(t *testing.T) {
		th, _ := e.startThread(sam, e.m1, e.cCasey, "To close", "Last word.")
		want(t, e.postJSON("/api/threads/"+th.ID+"/status", e.tok(sam), map[string]string{"status": "closed"}), http.StatusOK)
		want(t, e.replyAs(sam, th.ID, "One more."), http.StatusConflict)
		want(t, e.replyAs(avery, th.ID, "One more."), http.StatusConflict)
		arch, _ := e.startThread(sam, e.m1, e.cCasey, "Archive me", "Filed away.")
		want(t, e.postForm("/staff/threads/"+arch.ID+"/status", e.tok(sam), url.Values{"status": {"archived"}}), http.StatusOK)
		inbox := e.get("/staff/inbox", e.tok(sam))
		lacks(t, inbox.Body, arch.ID)
		has(t, inbox.Body, th.ID)
		has(t, e.get("/staff/inbox?status=archived", e.tok(sam)).Body, arch.ID)
		if _, ms := e.openAs(sam, arch.ID); len(ms) != 1 {
			t.Error("the archived thread isn't readable")
		}
		if e.events(e.m1, "client_portal.thread_status_changed") != 2 {
			t.Error("status changes left no timeline rows")
		}

		needs(t, clientReads)
		want(t, e.replyAs(casey, th.ID, "One more."), http.StatusConflict)
		if r := e.postForm("/client/messages/"+th.ID, e.tok(casey), url.Values{"body": {"Form reply."}}); r.Code != http.StatusConflict {
			t.Errorf("client form reply on a closed thread = %d", r.Code)
		}
		if _, ms := e.openAs(casey, arch.ID); len(ms) != 1 {
			t.Error("Casey can't read the archived thread")
		}
	})

	t.Run("MOD-POR-28", func(t *testing.T) {
		th, samMsg := e.startThread(sam, e.m1, e.cCasey, "Retract", "Wrong figure: 999.")
		r := e.replyAs(avery, th.ID, "Avery's words.")
		want(t, r, http.StatusCreated)
		var averyMsg struct {
			ID string `json:"id"`
		}
		r.json(t, &averyMsg)
		want(t, e.postJSON("/api/messages/"+samMsg.ID+"/retract", e.tok(sam), map[string]any{}), http.StatusOK)
		want(t, e.postJSON("/api/messages/"+averyMsg.ID+"/retract", e.tok(sam), map[string]any{}), http.StatusForbidden)
		_, ms := e.openAs(sam, th.ID)
		if !ms[0].Removed || ms[0].Body != "" || ms[1].Removed {
			t.Errorf("after retracting: %+v", ms)
		}

		needs(t, clientReads)
		r = e.replyAs(casey, th.ID, "Casey's own words.")
		var caseyMsg struct {
			ID string `json:"id"`
		}
		r.json(t, &caseyMsg)
		want(t, e.postJSON("/api/messages/"+caseyMsg.ID+"/retract", e.tok(sam), map[string]any{}), http.StatusForbidden)
		page := e.get("/client/messages?thread_id="+th.ID+"&matter_id="+e.m1, e.tok(casey))
		has(t, page.Body, "Message removed")
		lacks(t, page.Body, "Wrong figure: 999.")
	})

	t.Run("MOD-POR-29", func(t *testing.T) {
		e.startThread(sam, e.m1, e.cCasey, "Probate", "The courthouse wants the marmalade affidavit.")
		e.startThread(avery, e.m4, e.cCasey, "Walled", "The quarantined tangerine memo.")
		type hits struct {
			Data []struct {
				ThreadID string `json:"thread_id"`
				MatterID string `json:"matter_id"`
			} `json:"data"`
		}
		var out hits
		r := e.get("/api/search?q=marmalade", e.tok(sam))
		want(t, r, http.StatusOK)
		r.json(t, &out)
		if len(out.Data) != 1 || out.Data[0].MatterID != e.m1 {
			t.Errorf("Sam's search for an M1 word: %+v", out.Data)
		}
		out = hits{}
		e.get("/api/search?q=tangerine", e.tok(sam)).json(t, &out)
		if len(out.Data) != 0 {
			t.Errorf("Sam finds the M4 thread: %+v", out.Data)
		}
		out = hits{}
		e.get("/api/search?q=tangerine", e.tok(avery)).json(t, &out)
		if len(out.Data) != 1 {
			t.Errorf("Avery's search for the M4 word: %+v", out.Data)
		}
		has(t, e.get("/staff/search?q=marmalade", e.tok(sam)).Body, "Probate")
	})
}

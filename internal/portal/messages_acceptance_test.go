package portal_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu-client-portal/internal/fakecore"
)

// Messages (SPEC section 8, MOD-POR-22 to 29).

func (e *env) messageCount() int {
	return e.dbCount(`SELECT COUNT(*) FROM mod_client_portal.messages`)
}

func TestMessages(t *testing.T) {
	e := newEnv(t)

	t.Run("MOD-POR-22", func(t *testing.T) {
		th, msg := e.startThread(e.sam, e.m1.ID, e.cCasey.ID, "Welcome", "Welcome to the portal.")
		if th.MatterID != e.m1.ID || th.ContactID != e.cCasey.ID || th.Status != "active" || msg.AuthorKind != "staff" {
			t.Fatalf("thread %+v message %+v", th, msg)
		}
		// The same through the matter tab's form.
		r := e.postForm("/staff/threads", e.tok(e.sam, e.m1.ID), url.Values{"matter_id": {e.m1.ID}, "contact_id": {e.cCasey.ID}, "subject": {"Form"}, "body": {"From the form."}})
		want(t, r, http.StatusOK)
		has(t, r.Body, "From the form.")

		closed := e.fc.AddMatter("2026-0005", "Closed Matter", e.avery.ID, e.sam.ID)
		prospect := e.fc.AddMatter("2026-0006", "Prospect Matter", e.avery.ID, e.sam.ID)
		e.fc.Lock(func() {
			closed.Status, closed.ClosedAt = "closed", e.now.Unix()
			prospect.Status = "prospect"
		})
		e.fc.AddParty(closed.ID, e.cCasey.ID, "client")
		e.fc.AddParty(prospect.ID, e.cCasey.ID, "client")
		n := e.messageCount()
		for _, c := range []struct{ matter, contact string }{{closed.ID, e.cCasey.ID}, {prospect.ID, e.cCasey.ID}, {e.m1.ID, e.cDrew.ID}, {e.m1.ID, guessedID}} {
			r := e.postJSON("/api/threads", e.tok(e.sam), map[string]any{"matter_id": c.matter, "contact_id": c.contact, "body": "Hello"})
			want(t, r, http.StatusBadRequest)
		}
		if e.messageCount() != n {
			t.Error("a refused thread wrote a message")
		}
	})

	t.Run("MOD-POR-23", func(t *testing.T) {
		th, _ := e.startThread(e.sam, e.m1.ID, e.cCasey.ID, "Draft", "Here is the draft.", e.f1Plain.ID)
		if !e.f1Plain.Shared {
			t.Fatal("the attached file didn't become shared")
		}
		if n := e.fc.Count("client_portal.file_sent_to_client"); n != 1 {
			t.Fatalf("%d sent-to-client rows, want 1", n)
		}
		want(t, e.replyAs(e.sam, th.ID, "Again, the draft.", e.f1Plain.ID), http.StatusCreated)
		if n := e.fc.Count("client_portal.file_sent_to_client"); n != 2 {
			t.Fatalf("%d sent-to-client rows after attaching again, want 2", n)
		}
		_, ms := e.openAs(e.casey, th.ID)
		if len(ms) != 2 || len(ms[0].Attachments) != 1 || ms[0].Attachments[0].FileID != e.f1Plain.ID {
			t.Errorf("Casey's view of the attachment: %+v", ms)
		}
		n, rows := e.messageCount(), e.fc.Count("client_portal.file_sent_to_client")
		for _, f := range []*fakecore.File{e.f2, e.f1Atty} {
			r := e.replyAs(e.sam, th.ID, "Wrong file.", f.ID)
			want(t, r, http.StatusBadRequest)
			has(t, r.Body, "A document you attached can't be sent.")
			r = e.postJSON("/api/threads", e.tok(e.sam), map[string]any{"matter_id": e.m1.ID, "contact_id": e.cCasey.ID, "body": "Wrong file.", "file_ids": []string{f.ID}})
			want(t, r, http.StatusBadRequest)
		}
		if e.messageCount() != n || e.fc.Count("client_portal.file_sent_to_client") != rows || e.f2.Shared != true || e.f1Atty.Shared != true {
			t.Error("a refused attachment wrote something")
		}
	})

	t.Run("MOD-POR-24", func(t *testing.T) {
		e.fc.Reset()
		th, msg := e.startThread(e.sam, e.m1.ID, e.cCasey.ID, "Private", "The settlement figure is 4815162342.")
		if len(e.fc.Mail) != 1 {
			t.Fatalf("%d mails, want 1", len(e.fc.Mail))
		}
		m := e.fc.Mail[0]
		if m.ToUserID != e.casey.ID || !strings.HasPrefix(m.LinkPath, "/portal/client-portal/client-messages?") || !strings.Contains(m.LinkPath, th.ID) {
			t.Errorf("mail %+v", m)
		}
		if strings.Contains(m.Text, "4815162342") || strings.Contains(m.Subject, "4815162342") {
			t.Error("the mail carries the message body")
		}
		status := func(id string) string {
			var s string
			if err := e.db.Admin.QueryRow(`SELECT status FROM mod_client_portal.message_notifications WHERE message_id = $1`, id).Scan(&s); err != nil {
				t.Fatal(err)
			}
			return s
		}
		if s := status(msg.ID); s != "sent" {
			t.Errorf("notification row %q, want sent", s)
		}
		e.fc.Settings["email_client_on_staff_message"] = "false"
		r := e.replyAs(e.sam, th.ID, "No mail for this one.")
		want(t, r, http.StatusCreated)
		var reply struct {
			ID string `json:"id"`
		}
		r.json(t, &reply)
		if len(e.fc.Mail) != 1 {
			t.Error("mail went with email_client_on_staff_message off")
		}
		if s := status(reply.ID); s != "skipped" {
			t.Errorf("notification row %q, want skipped", s)
		}
		delete(e.fc.Settings, "email_client_on_staff_message")
		e.fc.MailFails = true
		r = e.replyAs(e.sam, th.ID, "Mail fails for this one.")
		want(t, r, http.StatusCreated)
		r.json(t, &reply)
		if s := status(reply.ID); s != "failed" {
			t.Errorf("notification row %q, want failed", s)
		}
		e.fc.MailFails = false
	})

	t.Run("MOD-POR-25", func(t *testing.T) {
		e.fc.Reset()
		e.setActive(e.casey, e.m1.ID)
		r := e.postForm("/client/messages", e.tok(e.casey), url.Values{"subject": {"Question"}, "body": {"My secret word is periwinkle."}})
		want(t, r, http.StatusOK)
		has(t, r.Body, "My secret word is periwinkle.")
		got := map[string]fakecore.Notification{}
		e.fc.Lock(func() {
			for _, n := range e.fc.Notifications {
				got[n.UserID] = n
			}
		})
		for _, u := range []*fakecore.User{e.avery, e.sam} {
			n, ok := got[u.ID]
			if !ok || n.Kind != "client-portal.client_message" || !n.Email || strings.Contains(n.Title, "periwinkle") {
				t.Errorf("%s notification %+v", u.Name, n)
			}
		}
		if len(got) != 2 {
			t.Errorf("%d people notified, want Avery and Sam", len(got))
		}
		if e.fc.Count("client_portal.message_sent") != 1 {
			t.Error("no message_sent timeline row for the client message")
		}
	})

	t.Run("MOD-POR-26", func(t *testing.T) {
		e.fc.Reset()
		th, msg := e.startThread(e.sam, e.m1.ID, e.cCasey.ID, "Receipt", "Please confirm.")
		_, ms := e.openAs(e.casey, th.ID)
		if ms[0].ID != msg.ID || ms[0].ReadAt == nil {
			t.Fatalf("Sam's message not read after Casey opened it: %+v", ms[0])
		}
		viewed := 0
		e.fc.Lock(func() {
			for _, ev := range e.fc.Events {
				if ev.Action == "client_portal.message_viewed" {
					viewed++
					if !ev.ClientVisible || ev.ActorKind != "client" {
						t.Errorf("viewed row %+v", ev)
					}
				}
			}
		})
		if viewed != 1 {
			t.Fatalf("%d viewed rows, want 1", viewed)
		}
		e.openAs(e.casey, th.ID)
		if e.fc.Count("client_portal.message_viewed") != 1 {
			t.Error("opening again wrote another viewed row")
		}
		base := e.badge(e.sam)
		want(t, e.replyAs(e.casey, th.ID, "Confirmed."), http.StatusCreated)
		if e.badge(e.sam) != base+1 {
			t.Fatal("Sam's badge didn't rise")
		}
		page := e.get("/staff/thread/"+th.ID, e.tok(e.sam))
		want(t, page, http.StatusOK)
		has(t, page.Body, "Confirmed.")
		if e.badge(e.sam) != base {
			t.Error("Sam opening the thread didn't lower the badge")
		}
		_, ms = e.openAs(e.sam, th.ID)
		if last := ms[len(ms)-1]; last.AuthorKind != "client" || last.ReadAt == nil {
			t.Errorf("Casey's message not read: %+v", last)
		}
		if e.fc.Count("client_portal.message_viewed") != 1 {
			t.Error("staff reading wrote a client viewed row")
		}
	})

	t.Run("MOD-POR-27", func(t *testing.T) {
		th, _ := e.startThread(e.sam, e.m1.ID, e.cCasey.ID, "To close", "Last word.")
		r := e.postJSON("/api/threads/"+th.ID+"/status", e.tok(e.sam), map[string]string{"status": "closed"})
		want(t, r, http.StatusOK)
		want(t, e.replyAs(e.sam, th.ID, "One more."), http.StatusConflict)
		want(t, e.replyAs(e.casey, th.ID, "One more."), http.StatusConflict)
		if r := e.postForm("/client/messages/"+th.ID, e.tok(e.casey), url.Values{"body": {"Form reply."}}); r.Code != http.StatusConflict {
			t.Errorf("client form reply on a closed thread = %d", r.Code)
		}
		arch, _ := e.startThread(e.sam, e.m1.ID, e.cCasey.ID, "Archive me", "Filed away.")
		r = e.postForm("/staff/threads/"+arch.ID+"/status", e.tok(e.sam), url.Values{"status": {"archived"}})
		want(t, r, http.StatusOK)
		inbox := e.get("/staff/inbox", e.tok(e.sam))
		lacks(t, inbox.Body, arch.ID)
		has(t, inbox.Body, th.ID)
		has(t, e.get("/staff/inbox?status=archived", e.tok(e.sam)).Body, arch.ID)
		_, ms := e.openAs(e.sam, arch.ID)
		_, cms := e.openAs(e.casey, arch.ID)
		if len(ms) != 1 || len(cms) != 1 {
			t.Error("the archived thread isn't readable")
		}
		want(t, e.replyAs(e.casey, arch.ID, "Still here."), http.StatusCreated)
		if e.fc.Count("client_portal.thread_status_changed") != 2 {
			t.Error("status changes left no timeline rows")
		}
	})

	t.Run("MOD-POR-28", func(t *testing.T) {
		th, samMsg := e.startThread(e.sam, e.m1.ID, e.cCasey.ID, "Retract", "Wrong figure: 999.")
		r := e.replyAs(e.casey, th.ID, "Casey's own words.")
		var caseyMsg struct {
			ID string `json:"id"`
		}
		r.json(t, &caseyMsg)
		r = e.replyAs(e.avery, th.ID, "Avery's words.")
		var averyMsg struct {
			ID string `json:"id"`
		}
		r.json(t, &averyMsg)
		want(t, e.postJSON("/api/messages/"+samMsg.ID+"/retract", e.tok(e.sam), map[string]any{}), http.StatusOK)
		_, ms := e.openAs(e.casey, th.ID)
		if !ms[0].Removed || ms[0].Body != "" {
			t.Errorf("Casey sees %+v", ms[0])
		}
		page := e.get("/client/messages?thread_id="+th.ID+"&matter_id="+e.m1.ID, e.tok(e.casey))
		has(t, page.Body, "Message removed")
		lacks(t, page.Body, "Wrong figure: 999.")
		for _, id := range []string{caseyMsg.ID, averyMsg.ID} {
			want(t, e.postJSON("/api/messages/"+id+"/retract", e.tok(e.sam), map[string]any{}), http.StatusForbidden)
		}
		_, ms = e.openAs(e.sam, th.ID)
		for _, m := range ms[1:] {
			if m.Removed {
				t.Errorf("message %s was removed", m.ID)
			}
		}
	})

	t.Run("MOD-POR-29", func(t *testing.T) {
		e.startThread(e.sam, e.m1.ID, e.cCasey.ID, "Probate", "The courthouse wants the marmalade affidavit.")
		e.startThread(e.avery, e.m4.ID, e.cCasey.ID, "Walled", "The quarantined tangerine memo.")
		var out struct {
			Hits []struct {
				ThreadID string `json:"thread_id"`
				MatterID string `json:"matter_id"`
			} `json:"data"`
		}
		r := e.get("/api/search?q=marmalade", e.tok(e.sam))
		want(t, r, http.StatusOK)
		r.json(t, &out)
		if len(out.Hits) != 1 || out.Hits[0].MatterID != e.m1.ID {
			t.Errorf("search for an M1 word: %+v", out.Hits)
		}
		out.Hits = nil
		r = e.get("/api/search?q=tangerine", e.tok(e.sam))
		r.json(t, &out)
		if len(out.Hits) != 0 {
			t.Errorf("Sam finds the M4 thread: %+v", out.Hits)
		}
		out.Hits = nil
		e.get("/api/search?q=tangerine", e.tok(e.avery)).json(t, &out)
		if len(out.Hits) != 1 {
			t.Errorf("Avery's search for the M4 word: %+v", out.Hits)
		}
		page := e.get("/staff/search?q=marmalade", e.tok(e.sam))
		has(t, page.Body, "Probate")
	})
}

package portal

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/CharitonMedia/togavsu-client-portal/internal/coreapi"
)

// Threads are scoped to one matter and one client contact. Either side can start one;
// staff start them only on open matters with a contact who is a client party. A
// thread is active, archived (out of the staff inbox, still readable) or closed (no
// new messages from either side).

// Thread is a message thread.
type Thread struct {
	ID            string `json:"id"`
	MatterID      string `json:"matter_id"`
	ContactID     string `json:"contact_id"`
	Subject       string `json:"subject"`
	CreatedBy     string `json:"created_by_user_id"`
	Status        string `json:"status"`
	LastMessageAt int64  `json:"last_message_at"`
	CreatedAt     int64  `json:"created_at"`
	Unread        int    `json:"unread"`
	MatterLabel   string `json:"matter_label,omitempty"`
	ContactName   string `json:"contact_name,omitempty"`
}

// Message is one message. A retracted message shows as removed and its body is never
// returned.
type Message struct {
	ID           string       `json:"id"`
	ThreadID     string       `json:"thread_id"`
	AuthorUserID string       `json:"author_user_id"`
	AuthorKind   string       `json:"author_kind"`
	AuthorName   string       `json:"author_name"`
	Body         string       `json:"body"`
	Removed      bool         `json:"removed"`
	ReadAt       *int64       `json:"read_at"`
	CreatedAt    int64        `json:"created_at"`
	Attachments  []Attachment `json:"attachments"`
}

// Attachment links a core file to a message.
type Attachment struct {
	FileID string `json:"file_id"`
	Name   string `json:"name"`
}

// removedText is what both sides see for a retracted message.
const removedText = "Message removed"

const threadCols = `id, matter_id, contact_id, subject, created_by_user_id, status, last_message_at, created_at`

func scanThread(row interface{ Scan(...any) error }) (Thread, error) {
	var t Thread
	err := row.Scan(&t.ID, &t.MatterID, &t.ContactID, &t.Subject, &t.CreatedBy, &t.Status, &t.LastMessageAt, &t.CreatedAt)
	return t, err
}

func (s *Server) loadThread(ctx context.Context, id string) (Thread, error) {
	if !validID(id) {
		return Thread{}, errNotFound
	}
	t, err := scanThread(s.c.DB.QueryRowContext(ctx, `SELECT `+threadCols+` FROM message_threads WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Thread{}, errNotFound
	}
	return t, err
}

// myContact is a client viewer's linked contact.
func (s *Server) myContact(ctx context.Context, v *viewer) (string, error) {
	me, err := s.me(ctx, v)
	if err != nil {
		return "", err
	}
	if me.LinkedContactID == nil || *me.LinkedContactID == "" {
		return "", errNotFound
	}
	return *me.LinkedContactID, nil
}

// visibleThread loads a thread the viewer may see: staff who can read its matter, or
// the client whose contact it is, while the core lets them read the matter. Anything
// else is 404, the same as a thread that doesn't exist.
func (s *Server) visibleThread(ctx context.Context, v *viewer, id string) (Thread, error) {
	t, err := s.loadThread(ctx, id)
	if err != nil {
		return Thread{}, err
	}
	if v.isClient() {
		mine, err := s.myContact(ctx, v)
		if err != nil {
			return Thread{}, err
		}
		if t.ContactID != mine {
			return Thread{}, errNotFound
		}
	}
	ok, err := s.canRead(ctx, v, t.MatterID)
	if err != nil {
		return Thread{}, err
	}
	if !ok {
		return Thread{}, errNotFound
	}
	return t, nil
}

// ThreadFilter narrows a thread list.
type ThreadFilter struct {
	MatterID  string
	ContactID string
	Status    string
}

// otherKind is the author kind whose messages the viewer reads.
func otherKind(v *viewer) string {
	if v.isClient() {
		return "staff"
	}
	return "client"
}

// listThreads returns the threads the viewer may see, newest message first, each with
// the count of the other side's unread messages. A staff list leaves out archived
// threads unless the filter asks for them.
func (s *Server) listThreads(ctx context.Context, v *viewer, f ThreadFilter) ([]Thread, error) {
	where := []string{"TRUE"}
	args := []any{otherKind(v)}
	add := func(cond string, val any) {
		args = append(args, val)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+itoa(len(args))))
	}
	if f.MatterID != "" {
		if !validID(f.MatterID) {
			return nil, errNotFound
		}
		add("t.matter_id = ?", f.MatterID)
	}
	if v.isClient() {
		mine, err := s.myContact(ctx, v)
		if err != nil {
			return nil, err
		}
		add("t.contact_id = ?", mine)
	} else if f.ContactID != "" {
		if !validID(f.ContactID) {
			return nil, errNotFound
		}
		add("t.contact_id = ?", f.ContactID)
	}
	switch f.Status {
	case "active", "archived", "closed":
		add("t.status = ?", f.Status)
	case "", "current":
		if !v.isClient() {
			where = append(where, "t.status <> 'archived'")
		}
	case "all":
	default:
		return nil, badRequest("status is active, archived, closed or all.")
	}
	rows, err := s.c.DB.QueryContext(ctx, `SELECT t.id, t.matter_id, t.contact_id, t.subject, t.created_by_user_id, t.status, t.last_message_at, t.created_at,
  (SELECT COUNT(*) FROM messages m WHERE m.thread_id = t.id AND m.author_kind = $1 AND m.read_at IS NULL AND m.status = 'active')
  FROM message_threads t WHERE `+strings.Join(where, " AND ")+` ORDER BY t.last_message_at DESC, t.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var all []Thread
	for rows.Next() {
		var t Thread
		if err := rows.Scan(&t.ID, &t.MatterID, &t.ContactID, &t.Subject, &t.CreatedBy, &t.Status, &t.LastMessageAt, &t.CreatedAt, &t.Unread); err != nil {
			return nil, err
		}
		all = append(all, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return s.readable(ctx, v, all)
}

// readable keeps the threads whose matter the core lets the viewer read, and labels
// each with its matter and contact.
func (s *Server) readable(ctx context.Context, v *viewer, ts []Thread) ([]Thread, error) {
	var out []Thread
	for _, t := range ts {
		m, err := s.matter(ctx, v, t.MatterID)
		if coreapi.IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		t.MatterLabel = m.Label()
		if !v.isClient() {
			if c, err := s.contact(ctx, v, t.ContactID); err == nil {
				t.ContactName = c.Name()
			}
		}
		out = append(out, t)
	}
	return out, nil
}

// messages reads a thread's messages, oldest first, with attachments and author names.
func (s *Server) messages(ctx context.Context, v *viewer, t Thread) ([]Message, error) {
	rows, err := s.c.DB.QueryContext(ctx, `SELECT id, thread_id, author_user_id, author_kind, body, status, read_at, created_at
  FROM messages WHERE thread_id = $1 ORDER BY created_at, id`, t.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	var staffIDs []string
	for rows.Next() {
		var m Message
		var status string
		var read sql.NullInt64
		if err := rows.Scan(&m.ID, &m.ThreadID, &m.AuthorUserID, &m.AuthorKind, &m.Body, &status, &read, &m.CreatedAt); err != nil {
			return nil, err
		}
		if read.Valid {
			r := read.Int64
			m.ReadAt = &r
		}
		if status == "retracted" {
			m.Removed, m.Body = true, ""
		}
		if m.AuthorKind == "staff" {
			staffIDs = append(staffIDs, m.AuthorUserID)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	names := s.staffNames(ctx, v, staffIDs)
	clientName := ""
	if v.isClient() {
		if me, err := s.me(ctx, v); err == nil {
			clientName = me.Name
		}
	} else if c, err := s.contact(ctx, v, t.ContactID); err == nil {
		clientName = c.Name()
	}
	atts, err := s.attachments(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].AuthorKind == "staff" {
			out[i].AuthorName = names[out[i].AuthorUserID]
		} else {
			out[i].AuthorName = clientName
		}
		if !out[i].Removed {
			for _, a := range atts[out[i].ID] {
				if f, err := s.file(ctx, v, a.FileID); err == nil {
					a.Name = f.Name
					out[i].Attachments = append(out[i].Attachments, a)
				}
			}
		}
	}
	return out, nil
}

func (s *Server) attachments(ctx context.Context, thread string) (map[string][]Attachment, error) {
	rows, err := s.c.DB.QueryContext(ctx, `SELECT a.message_id, a.file_id FROM message_attachments a JOIN messages m ON m.id = a.message_id
  WHERE m.thread_id = $1 ORDER BY a.created_at, a.file_id`, thread)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]Attachment{}
	for rows.Next() {
		var msg, file string
		if err := rows.Scan(&msg, &file); err != nil {
			return nil, err
		}
		out[msg] = append(out[msg], Attachment{FileID: file})
	}
	return out, rows.Err()
}

// openThread returns a thread with its messages and stamps the other side's unread
// messages read. When a client's opening stamps any staff message, one client-visible
// "viewed" row goes on the timeline.
func (s *Server) openThread(ctx context.Context, v *viewer, id string) (Thread, []Message, error) {
	t, err := s.visibleThread(ctx, v, id)
	if err != nil {
		return Thread{}, nil, err
	}
	res, err := s.c.DB.ExecContext(ctx, `UPDATE messages SET read_at = $1, updated_at = $1
  WHERE thread_id = $2 AND author_kind = $3 AND read_at IS NULL AND status = 'active'`, s.now(), t.ID, otherKind(v))
	if err != nil {
		return Thread{}, nil, err
	}
	if n, _ := res.RowsAffected(); n > 0 && v.isClient() {
		s.event(ctx, v, "client_portal.message_viewed", "client_portal.thread", t.ID, t.MatterID, "Client viewed portal messages", true)
	}
	ms, err := s.messages(ctx, v, t)
	if err != nil {
		return Thread{}, nil, err
	}
	m, _ := s.matter(ctx, v, t.MatterID)
	t.MatterLabel = m.Label()
	if !v.isClient() {
		if c, err := s.contact(ctx, v, t.ContactID); err == nil {
			t.ContactName = c.Name()
		}
	}
	return t, ms, nil
}

// NewThread is a request to start a thread.
type NewThread struct {
	MatterID  string   `json:"matter_id"`
	ContactID string   `json:"contact_id"`
	Subject   string   `json:"subject"`
	Body      string   `json:"body"`
	FileIDs   []string `json:"file_ids"`
}

// maxSubjectChars bounds a thread subject.
const maxSubjectChars = 200

func (s *Server) checkBody(ctx context.Context, body string) (string, error) {
	set, err := s.settings(ctx)
	if err != nil {
		return "", err
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return "", badRequest("Write a message first.")
	}
	if utf8.RuneCountInString(body) > set.MessageMaxChars {
		return "", badRequest("That message is longer than " + itoa(set.MessageMaxChars) + " characters.")
	}
	return body, nil
}

// startThread starts a thread with its first message. A client's thread is always with
// their own contact, on a matter the core lets them read. Staff start threads only on
// open matters with a client party.
func (s *Server) startThread(ctx context.Context, v *viewer, in NewThread) (Thread, Message, error) {
	body, err := s.checkBody(ctx, in.Body)
	if err != nil {
		return Thread{}, Message{}, err
	}
	in.Subject = strings.TrimSpace(in.Subject)
	if utf8.RuneCountInString(in.Subject) > maxSubjectChars {
		return Thread{}, Message{}, badRequest("The subject is longer than 200 characters.")
	}
	if in.MatterID == "" {
		return Thread{}, Message{}, &apiError{Status: http.StatusBadRequest, Code: "matter_required", Message: "Choose a matter first."}
	}
	m, err := s.matter(ctx, v, in.MatterID)
	if err != nil {
		return Thread{}, Message{}, err
	}
	if v.isClient() {
		mine, err := s.myContact(ctx, v)
		if err != nil {
			return Thread{}, Message{}, err
		}
		if in.ContactID != "" && in.ContactID != mine {
			return Thread{}, Message{}, errNotFound
		}
		in.ContactID = mine
	} else {
		if m.Status != "open" {
			return Thread{}, Message{}, badRequest("Messages start only on open matters.")
		}
		if !validID(in.ContactID) {
			return Thread{}, Message{}, badRequest("Choose a client on this matter.")
		}
		ps, err := s.clientParties(ctx, v, m.ID)
		if err != nil {
			return Thread{}, Message{}, err
		}
		found := false
		for _, p := range ps {
			found = found || p.ContactID == in.ContactID
		}
		if !found {
			return Thread{}, Message{}, badRequest("That contact isn't a client on this matter.")
		}
	}
	files, err := s.checkAttachments(ctx, v, m.ID, in.FileIDs)
	if err != nil {
		return Thread{}, Message{}, err
	}
	if err := s.shareFiles(ctx, v, files); err != nil {
		return Thread{}, Message{}, err
	}
	now := s.now()
	t := Thread{ID: newID(s.c.Now()), MatterID: m.ID, ContactID: in.ContactID, Subject: in.Subject, CreatedBy: v.UserID,
		Status: "active", LastMessageAt: now, CreatedAt: now, MatterLabel: m.Label()}
	msg, err := s.writeMessage(ctx, v, t, true, body, files)
	if err != nil {
		return Thread{}, Message{}, err
	}
	s.afterMessage(ctx, v, t, msg, files)
	return t, msg, nil
}

// reply adds a message to a thread. A closed thread takes none (409).
func (s *Server) reply(ctx context.Context, v *viewer, threadID, body string, fileIDs []string) (Message, error) {
	t, err := s.visibleThread(ctx, v, threadID)
	if err != nil {
		return Message{}, err
	}
	if t.Status == "closed" {
		return Message{}, conflict("This conversation is closed.")
	}
	body, err = s.checkBody(ctx, body)
	if err != nil {
		return Message{}, err
	}
	files, err := s.checkAttachments(ctx, v, t.MatterID, fileIDs)
	if err != nil {
		return Message{}, err
	}
	if err := s.shareFiles(ctx, v, files); err != nil {
		return Message{}, err
	}
	msg, err := s.writeMessage(ctx, v, t, false, body, files)
	if err != nil {
		return Message{}, err
	}
	s.afterMessage(ctx, v, t, msg, files)
	return msg, nil
}

// checkAttachments reads each file as the viewer and refuses the whole send on one bad
// id: a file the viewer can't read, on another matter, attorney-only or trashed. A
// client may attach only a file the core already shows them on that matter.
func (s *Server) checkAttachments(ctx context.Context, v *viewer, matter string, ids []string) ([]File, error) {
	var out []File
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		f, err := s.file(ctx, v, id)
		if coreapi.IsNotFound(err) || coreapi.StatusOf(err) == http.StatusForbidden {
			return nil, badRequest("A document you attached can't be sent.")
		}
		if err != nil {
			return nil, err
		}
		if !f.OnMatter(matter) || f.AttorneyOnly || f.TrashedAt != nil || (v.isClient() && !f.SharedWithClient) {
			return nil, badRequest("A document you attached can't be sent.")
		}
		out = append(out, f)
	}
	return out, nil
}

// shareFiles shares each staff attachment with the client: attaching a document sends
// it, so an unshared one becomes shared first.
func (s *Server) shareFiles(ctx context.Context, v *viewer, files []File) error {
	if v.isClient() {
		return nil
	}
	for i, f := range files {
		if f.SharedWithClient {
			continue
		}
		if err := s.api.Patch(ctx, v.Token, "/api/v1/files/"+f.ID, map[string]any{"shared_with_client": true}, nil); err != nil {
			if coreapi.IsNotFound(err) || coreapi.StatusOf(err) == http.StatusForbidden {
				return badRequest("A document you attached can't be sent.")
			}
			return err
		}
		files[i].SharedWithClient = true
	}
	return nil
}

// writeMessage stores a message (and, for a new thread, the thread) in one
// transaction.
func (s *Server) writeMessage(ctx context.Context, v *viewer, t Thread, newThread bool, body string, files []File) (Message, error) {
	now := s.now()
	kind := "staff"
	if v.isClient() {
		kind = "client"
	}
	msg := Message{ID: newID(s.c.Now()), ThreadID: t.ID, AuthorUserID: v.UserID, AuthorKind: kind, Body: body, CreatedAt: now}
	tx, err := s.c.DB.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if newThread {
		if _, err := tx.ExecContext(ctx, `INSERT INTO message_threads (`+threadCols+`, updated_at) VALUES ($1, $2, $3, $4, $5, 'active', $6, $6, $6)`,
			t.ID, t.MatterID, t.ContactID, t.Subject, t.CreatedBy, now); err != nil {
			return Message{}, err
		}
	} else {
		// The status check repeats inside the transaction, so a close that lands
		// between the read and the write still refuses the message.
		res, err := tx.ExecContext(ctx, `UPDATE message_threads SET last_message_at = $1, updated_at = $1 WHERE id = $2 AND status <> 'closed'`, now, t.ID)
		if err != nil {
			return Message{}, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return Message{}, conflict("This conversation is closed.")
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO messages (id, thread_id, author_user_id, author_kind, body, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $6)`,
		msg.ID, t.ID, v.UserID, kind, body, now); err != nil {
		return Message{}, err
	}
	for _, f := range files {
		if _, err := tx.ExecContext(ctx, `INSERT INTO message_attachments (message_id, file_id, created_at) VALUES ($1, $2, $3)`, msg.ID, f.ID, now); err != nil {
			return Message{}, err
		}
		msg.Attachments = append(msg.Attachments, Attachment{FileID: f.ID, Name: f.Name})
	}
	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	if me, err := s.me(ctx, v); err == nil {
		msg.AuthorName = me.Name
	}
	return msg, nil
}

// afterMessage writes the timeline rows and tells the other side: a staff message
// emails the client a portal link (never the body); a client message notifies the
// matter team (never the body).
func (s *Server) afterMessage(ctx context.Context, v *viewer, t Thread, msg Message, files []File) {
	if v.isClient() {
		s.event(ctx, v, "client_portal.message_sent", "client_portal.message", msg.ID, t.MatterID, "Client sent a portal message", true)
		s.notifyTeam(ctx, v, t, msg)
		return
	}
	s.event(ctx, v, "client_portal.message_sent", "client_portal.message", msg.ID, t.MatterID, "Portal message sent to the client", true)
	for _, f := range files {
		s.event(ctx, v, "client_portal.file_sent_to_client", "file", f.ID, t.MatterID, "Document sent to the client: "+f.Name, true)
	}
	s.emailClient(ctx, v, t, msg)
}

func (s *Server) recordNotice(ctx context.Context, msg, status, detail string) {
	if _, err := s.c.DB.ExecContext(ctx, `INSERT INTO message_notifications (message_id, status, detail, attempted_at) VALUES ($1, $2, $3, $4)
  ON CONFLICT (message_id) DO UPDATE SET status = EXCLUDED.status, detail = EXCLUDED.detail, attempted_at = EXCLUDED.attempted_at`, msg, status, detail, s.now()); err != nil {
		s.logf("client-portal: notice row for %s: %v", msg, err)
	}
}

// emailClient mails the thread's client user a portal link through core system mail
// (CORE-04). The module never sees an address; the result goes in
// message_notifications.
func (s *Server) emailClient(ctx context.Context, v *viewer, t Thread, msg Message) {
	set, err := s.settings(ctx)
	if err != nil {
		s.recordNotice(ctx, msg.ID, "failed", "settings unavailable")
		return
	}
	if !set.EmailClientOnStaffMessage {
		s.recordNotice(ctx, msg.ID, "skipped", "email to clients is off")
		return
	}
	var acc ClientAccess
	if err := s.api.Get(ctx, v.Token, "/api/v1/client-access?contact_id="+t.ContactID, &acc); err != nil {
		if coreapi.IsNotFound(err) {
			s.recordNotice(ctx, msg.ID, "skipped", "the client has no portal access")
			return
		}
		s.recordNotice(ctx, msg.ID, "failed", "client access unavailable")
		return
	}
	if !acc.Active {
		s.recordNotice(ctx, msg.ID, "skipped", "the client's portal access is off")
		return
	}
	firm := s.firm(ctx, v)
	body := map[string]any{
		"to_user_ids": []string{acc.UserID},
		"matter_id":   t.MatterID,
		"subject":     "New message from " + firm.Short(),
		"text":        "You have a new message from " + firm.Name + " in your client portal. Sign in to read it.",
		"link_path":   portalPath(ID, "client-messages", "matter_id", t.MatterID, "thread_id", t.ID),
	}
	var out struct {
		Results []struct {
			UserID string `json:"user_id"`
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"results"`
	}
	if err := s.api.Post(ctx, v.Token, "/api/v1/mail/messages", body, &out); err != nil {
		s.recordNotice(ctx, msg.ID, "failed", "mail unavailable")
		return
	}
	status, detail := "failed", "no result"
	for _, r := range out.Results {
		if r.UserID == acc.UserID {
			status, detail = r.Status, r.Error
		}
	}
	if status != "sent" && status != "skipped" {
		status = "failed"
	}
	s.recordNotice(ctx, msg.ID, status, detail)
}

// notifyTeam tells the matter's responsible attorney and assigned staff that a client
// wrote, as a service call (CORE-03): the module, not the client, is the sender.
func (s *Server) notifyTeam(ctx context.Context, v *viewer, t Thread, msg Message) {
	name := "A client"
	if me, err := s.me(ctx, v); err == nil && me.Name != "" {
		name = me.Name
	}
	body := map[string]any{"kind": "client_message", "title": name + " sent a portal message",
		"link":     "/modules/" + ID + "/tabs/messages?matter_id=" + t.MatterID + "&thread_id=" + t.ID,
		"audience": "matter_team", "matter_id": t.MatterID}
	if err := s.api.Post(ctx, "", "/api/v1/notifications", body, nil); err != nil {
		s.logf("client-portal: notify matter team on %s: %v", t.MatterID, err)
		s.recordNotice(ctx, msg.ID, "failed", "notification unavailable")
		return
	}
	s.recordNotice(ctx, msg.ID, "sent", "matter team notified")
}

// setStatus moves a thread to active, archived or closed (staff only).
func (s *Server) setStatus(ctx context.Context, v *viewer, id, status string) (Thread, error) {
	switch status {
	case "active", "archived", "closed":
	default:
		return Thread{}, badRequest("status is active, archived or closed.")
	}
	t, err := s.visibleThread(ctx, v, id)
	if err != nil {
		return Thread{}, err
	}
	if t.Status == status {
		return t, nil
	}
	if _, err := s.c.DB.ExecContext(ctx, `UPDATE message_threads SET status = $1, updated_at = $2 WHERE id = $3`, status, s.now(), t.ID); err != nil {
		return Thread{}, err
	}
	words := map[string]string{"active": "reopened", "archived": "archived", "closed": "closed"}
	s.event(ctx, v, "client_portal.thread_status_changed", "client_portal.thread", t.ID, t.MatterID, "Portal conversation "+words[status], false)
	t.Status = status
	return t, nil
}

// retract removes a staff member's own message. Nobody retracts another person's.
func (s *Server) retract(ctx context.Context, v *viewer, id string) error {
	if !validID(id) {
		return errNotFound
	}
	var thread, author, kind, status string
	err := s.c.DB.QueryRowContext(ctx, `SELECT thread_id, author_user_id, author_kind, status FROM messages WHERE id = $1`, id).Scan(&thread, &author, &kind, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	t, err := s.visibleThread(ctx, v, thread)
	if err != nil {
		return err
	}
	if author != v.UserID || kind != "staff" {
		return &apiError{Status: http.StatusForbidden, Code: "forbidden", Message: "You can retract only your own messages."}
	}
	if status == "retracted" {
		return nil
	}
	if _, err := s.c.DB.ExecContext(ctx, `UPDATE messages SET status = 'retracted', updated_at = $1 WHERE id = $2`, s.now(), id); err != nil {
		return err
	}
	s.event(ctx, v, "client_portal.message_retracted", "client_portal.message", id, t.MatterID, "Portal message retracted", false)
	return nil
}

// unreadCount is the number of unread client messages on matters the viewer reads.
func (s *Server) unreadCount(ctx context.Context, v *viewer) (int, error) {
	rows, err := s.c.DB.QueryContext(ctx, `SELECT t.matter_id, COUNT(*) FROM messages m JOIN message_threads t ON t.id = m.thread_id
  WHERE m.author_kind = 'client' AND m.read_at IS NULL AND m.status = 'active' GROUP BY t.matter_id`)
	if err != nil {
		return 0, err
	}
	type row struct {
		matter string
		n      int
	}
	var rs []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.matter, &r.n); err != nil {
			rows.Close()
			return 0, err
		}
		rs = append(rs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	total := 0
	for _, r := range rs {
		ok, err := s.canRead(ctx, v, r.matter)
		if err != nil {
			return 0, err
		}
		if ok {
			total += r.n
		}
	}
	return total, nil
}

// maxSearchHits bounds a search answer.
const maxSearchHits = 50

// search finds threads whose subject or a message body holds q, on matters the viewer
// reads. Retracted messages aren't searched.
func (s *Server) search(ctx context.Context, v *viewer, q string) ([]Thread, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	pat := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
	rows, err := s.c.DB.QueryContext(ctx, `SELECT `+threadCols+` FROM message_threads t WHERE t.subject ILIKE $1
  OR EXISTS (SELECT 1 FROM messages m WHERE m.thread_id = t.id AND m.status = 'active' AND m.body ILIKE $1)
  ORDER BY t.last_message_at DESC, t.id DESC`, pat)
	if err != nil {
		return nil, err
	}
	var all []Thread
	for rows.Next() {
		t, err := scanThread(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out, err := s.readable(ctx, v, all)
	if len(out) > maxSearchHits {
		out = out[:maxSearchHits]
	}
	return out, err
}

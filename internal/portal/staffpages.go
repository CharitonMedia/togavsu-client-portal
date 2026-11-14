package portal

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

const staffTemplates = `
{{define "thread-list"}}<ul class="threads">{{range .Threads}}<li data-thread="{{.ID}}"><a href="{{link $.V (print "/staff/thread/" .ID)}}">{{if .Subject}}{{.Subject}}{{else}}Conversation{{end}}</a>
  <span class="who">{{.ContactName}}</span> <span class="matter">{{.MatterLabel}}</span>{{if .Unread}} <span class="unread" data-unread="{{.Unread}}">{{.Unread}} unread</span>{{end}}{{if ne .Status "active"}} <span class="status">{{.Status}}</span>{{end}}</li>{{else}}<li>No conversations.</li>{{end}}</ul>{{end}}

{{define "inbox"}}{{template "staff-top" .}}
<h1>Messages</h1>
<form method="get" action="/m/client-portal/staff/inbox" class="filters">
  <input type="hidden" name="context" value="{{.V.Token}}">
  <label>Matter <select name="matter_id"><option value="">All matters</option>{{range .Body.Matters}}<option value="{{.ID}}"{{if eq .ID $.Body.Filter.MatterID}} selected{{end}}>{{.Label}}</option>{{end}}</select></label>
  <label>Status <select name="status"><option value="">Current</option><option value="active"{{if eq .Body.Filter.Status "active"}} selected{{end}}>Active</option><option value="closed"{{if eq .Body.Filter.Status "closed"}} selected{{end}}>Closed</option><option value="archived"{{if eq .Body.Filter.Status "archived"}} selected{{end}}>Archived</option></select></label>
  {{if .Body.Filter.ContactID}}<input type="hidden" name="contact_id" value="{{.Body.Filter.ContactID}}">{{end}}
  <button>Filter</button></form>
<p><a class="button" href="{{link .V "/staff/new"}}">New message</a></p>
{{template "thread-list" .Body.List}}
{{template "bottom"}}{{end}}

{{define "staff-thread"}}{{template "staff-top" .}}
{{with .Body.Thread}}<section class="thread" data-thread="{{.ID}}" data-status="{{.Status}}">
<h1>{{if .Subject}}{{.Subject}}{{else}}Conversation{{end}}</h1>
<p><span class="who">{{.ContactName}}</span> · <span class="matter">{{.MatterLabel}}</span> · {{.Status}}</p>
{{end}}
<ol class="messages">{{range .Body.Messages}}<li class="msg {{.AuthorKind}}" id="msg-{{.ID}}">
  <div class="meta"><strong>{{.AuthorName}}</strong> <time>{{when .CreatedAt}}</time>{{if .ReadAt}} <span class="read">Read</span>{{end}}</div>
  {{if .Removed}}<p class="removed">Message removed</p>{{else}}<p class="body">{{.Body}}</p>
  {{if .Attachments}}<ul class="attachments">{{range .Attachments}}<li data-file="{{.FileID}}">{{.Name}}</li>{{end}}</ul>{{end}}
  {{if and (eq .AuthorKind "staff") (eq .AuthorUserID $.V.UserID)}}<form method="post" action="{{link $.V (print "/staff/messages/" .ID "/retract")}}" hx-post="{{link $.V (print "/staff/messages/" .ID "/retract")}}" hx-target="body" hx-confirm="Retract this message?"><button>Retract</button></form>{{end}}{{end}}
</li>{{end}}</ol>
{{with .Body.Thread}}
{{if eq .Status "closed"}}<p>This conversation is closed.</p>{{else}}
<form method="post" action="{{link $.V (print "/staff/threads/" .ID "/messages")}}" hx-post="{{link $.V (print "/staff/threads/" .ID "/messages")}}" hx-target="body">
  <label>Reply<textarea name="body" required maxlength="{{$.Body.Max}}"></textarea></label>
  {{if $.Body.Files}}<fieldset><legend>Attach documents (attaching shares the document with the client)</legend>{{range $.Body.Files}}<label><input type="checkbox" name="file_ids" value="{{.ID}}"> {{.Name}}{{if not .SharedWithClient}} (not shared yet){{end}}</label>{{end}}</fieldset>{{end}}
  <button>Send</button></form>{{end}}
<form method="post" class="status" action="{{link $.V (print "/staff/threads/" .ID "/status")}}" hx-post="{{link $.V (print "/staff/threads/" .ID "/status")}}" hx-target="body">
  {{if ne .Status "active"}}<button name="status" value="active">Reopen</button>{{end}}
  {{if ne .Status "archived"}}<button name="status" value="archived">Archive</button>{{end}}
  {{if ne .Status "closed"}}<button name="status" value="closed">Close</button>{{end}}
</form>{{end}}
{{template "bottom"}}{{end}}

{{define "new-thread-form"}}<form method="post" action="{{link .V "/staff/threads"}}" hx-post="{{link .V "/staff/threads"}}" hx-target="body" class="new-thread">
  {{if .Body.Matter}}<input type="hidden" name="matter_id" value="{{.Body.Matter.ID}}">{{else}}
  <label>Matter <select name="matter_id" required hx-get="/m/client-portal/staff/new" hx-target="body" hx-trigger="change">
    <option value="">Choose an open matter</option>{{range .Body.Matters}}<option value="{{.ID}}">{{.Label}}</option>{{end}}</select></label>{{end}}
  {{if .Body.Clients}}<label>Client <select name="contact_id" required>{{range .Body.Clients}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select></label>
  <label>Subject<input name="subject" maxlength="200"></label>
  <label>Message<textarea name="body" required maxlength="{{.Body.Max}}"></textarea></label>
  {{if .Body.Files}}<fieldset><legend>Attach documents (attaching shares the document with the client)</legend>{{range .Body.Files}}<label><input type="checkbox" name="file_ids" value="{{.ID}}"> {{.Name}}</label>{{end}}</fieldset>{{end}}
  <button>Send</button>{{else if .Body.Matter}}<p>This matter has no client party to write to.</p>{{end}}
</form>{{end}}

{{define "matter-messages"}}{{template "staff-top" .}}
<h1>Messages</h1>
{{template "thread-list" .Body.List}}
{{if eq .Body.Matter.Status "open"}}<details class="new"><summary>New message</summary>{{template "new-thread-form" .}}</details>{{else}}<p>Messages start only on open matters.</p>{{end}}
{{template "bottom"}}{{end}}

{{define "staff-new"}}{{template "staff-top" .}}
<h1>New message</h1>
{{template "new-thread-form" .}}
{{template "bottom"}}{{end}}

{{define "widget"}}{{template "staff-top" .}}
<p class="widget-count" data-unread="{{.Body.Unread}}">{{.Body.Unread}} unread client messages</p>
{{template "thread-list" .Body.List}}
{{template "bottom"}}{{end}}

{{define "search"}}{{template "staff-top" .}}
<h1>Messages matching "{{.Body.Q}}"</h1>
{{template "thread-list" .Body.List}}
{{template "bottom"}}{{end}}

{{define "access"}}{{template "staff-top" .}}
<h1>Portal access</h1>
{{with .Body.Password}}<div class="once" role="alert"><p>Temporary password for {{.Email}}: <code data-temporary-password>{{.TemporaryPassword}}</code></p><p>It won't be shown again. Read it to the client; they set a new password at their next sign-in.</p></div>{{end}}
<ul class="access">{{range .Body.Rows}}<li data-contact="{{.ContactID}}"><strong>{{.Name}}</strong>
  {{if .Access}}<span class="state">{{if .Access.Active}}Portal on{{else}}Portal off{{end}} · {{.Access.Email}}</span>{{else}}<span class="state">No portal access</span>{{end}}
  {{with index $.Body.Sent .ContactID}}<span class="sent" data-mail="{{.MailStatus}}">{{if eq .MailStatus "sent"}}Sent to {{.Email}}{{else}}Not sent{{end}}</span>{{end}}
  <form method="post" hx-target="body" action="{{link $.V (print "/staff/access/" .ContactID "/grant")}}" hx-post="{{link $.V (print "/staff/access/" .ContactID "/grant")}}"><button>Send portal access</button></form>
  {{if .Access}}<form method="post" hx-target="body" action="{{link $.V (print "/staff/access/" .ContactID "/temporary-password")}}" hx-post="{{link $.V (print "/staff/access/" .ContactID "/temporary-password")}}"><button>Temporary password</button></form>
  {{if .Access.Active}}<form method="post" hx-target="body" action="{{link $.V (print "/staff/access/" .ContactID "/deactivate")}}" hx-post="{{link $.V (print "/staff/access/" .ContactID "/deactivate")}}"><button>Turn off</button></form>
  {{else}}<form method="post" hx-target="body" action="{{link $.V (print "/staff/access/" .ContactID "/reactivate")}}" hx-post="{{link $.V (print "/staff/access/" .ContactID "/reactivate")}}"><button>Turn on</button></form>{{end}}{{end}}
</li>{{else}}<li>This matter has no client party.</li>{{end}}</ul>
{{template "bottom"}}{{end}}
`

// staffData is what a staff page draws.
type staffData struct {
	Title         string
	V             *viewer
	Error, Notice string
	Body          any
}

func (s *Server) staffRender(w http.ResponseWriter, status int, name string, d staffData) {
	render(w, status, name, d)
}

// --- inbox, matter tab, widget, badge, search ---

func filterFrom(r *http.Request) ThreadFilter {
	q := r.URL.Query()
	return ThreadFilter{MatterID: q.Get("matter_id"), ContactID: q.Get("contact_id"), Status: q.Get("status")}
}

func (s *Server) inboxPage(w http.ResponseWriter, r *http.Request, v *viewer) {
	if id := r.URL.Query().Get("thread_id"); id != "" {
		s.staffThreadView(w, r, v, id, http.StatusOK, "", "")
		return
	}
	f := filterFrom(r)
	ts, err := s.listThreads(r.Context(), v, f)
	if err != nil {
		s.fail(w, r, false, err)
		return
	}
	// The matter filter lists the matters that have threads the viewer can read.
	var matters []Matter
	seen := map[string]bool{}
	all, err := s.listThreads(r.Context(), v, ThreadFilter{Status: "all"})
	if err != nil {
		s.fail(w, r, false, err)
		return
	}
	for _, t := range all {
		if !seen[t.MatterID] {
			seen[t.MatterID] = true
			if m, err := s.matter(r.Context(), v, t.MatterID); err == nil {
				matters = append(matters, m)
			}
		}
	}
	s.staffRender(w, http.StatusOK, "inbox", staffData{Title: "Messages", V: v, Body: map[string]any{
		"List": listData{V: v, Threads: ts}, "Matters": matters, "Filter": f}})
}

// listData carries a thread list with the viewer for links.
type listData struct {
	V       *viewer
	Threads []Thread
}

func (s *Server) matterMessagesPage(w http.ResponseWriter, r *http.Request, v *viewer) {
	if v.MatterID == "" {
		renderMessage(w, http.StatusBadRequest, "Open this tab from a matter.")
		return
	}
	if id := r.URL.Query().Get("thread_id"); id != "" {
		s.staffThreadView(w, r, v, id, http.StatusOK, "", "")
		return
	}
	s.matterMessagesView(w, r, v, http.StatusOK, "", "")
}

func (s *Server) matterMessagesView(w http.ResponseWriter, r *http.Request, v *viewer, status int, note, errText string) {
	ctx := r.Context()
	m, err := s.matter(ctx, v, v.MatterID)
	if err != nil {
		s.fail(w, r, false, err)
		return
	}
	ts, err := s.listThreads(ctx, v, ThreadFilter{MatterID: m.ID, Status: "all"})
	if err != nil {
		s.fail(w, r, false, err)
		return
	}
	body, err := s.newThreadBody(ctx, v, m.ID)
	if err != nil {
		s.fail(w, r, false, err)
		return
	}
	body["List"] = listData{V: v, Threads: ts}
	s.staffRender(w, status, "matter-messages", staffData{Title: "Messages", V: v, Notice: note, Error: errText, Body: body})
}

type clientOption struct{ ID, Name string }

// newThreadBody fills the new-message form: for a chosen matter, its client parties
// and its documents; otherwise the open matters to choose from.
func (s *Server) newThreadBody(ctx context.Context, v *viewer, matter string) (map[string]any, error) {
	set, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"Max": set.MessageMaxChars}
	if matter == "" {
		ms, err := s.listMatters(ctx, v, "status=open")
		if err != nil {
			return nil, err
		}
		body["Matters"] = ms
		return body, nil
	}
	m, err := s.matter(ctx, v, matter)
	if err != nil {
		return nil, err
	}
	body["Matter"] = m
	ps, err := s.clientParties(ctx, v, m.ID)
	if err != nil {
		return nil, err
	}
	var clients []clientOption
	for _, p := range ps {
		name := p.ContactID
		if c, err := s.contact(ctx, v, p.ContactID); err == nil {
			name = c.Name()
		}
		clients = append(clients, clientOption{ID: p.ContactID, Name: name})
	}
	body["Clients"] = clients
	fs, err := s.files(ctx, v, m.ID)
	if err != nil {
		return nil, err
	}
	var attachable []File
	for _, f := range fs {
		if !f.AttorneyOnly && f.TrashedAt == nil && f.OnMatter(m.ID) {
			attachable = append(attachable, f)
		}
	}
	body["Files"] = attachable
	return body, nil
}

func (s *Server) newMessagePage(w http.ResponseWriter, r *http.Request, v *viewer) {
	body, err := s.newThreadBody(r.Context(), v, r.URL.Query().Get("matter_id"))
	if err != nil {
		s.fail(w, r, false, err)
		return
	}
	s.staffRender(w, http.StatusOK, "staff-new", staffData{Title: "New message", V: v, Body: body})
}

func (s *Server) widgetPage(w http.ResponseWriter, r *http.Request, v *viewer) {
	ts, err := s.listThreads(r.Context(), v, ThreadFilter{MatterID: r.URL.Query().Get("matter_id")})
	if err != nil {
		s.fail(w, r, false, err)
		return
	}
	unread := 0
	var withUnread []Thread
	for _, t := range ts {
		unread += t.Unread
		if t.Unread > 0 && len(withUnread) < widgetRows {
			withUnread = append(withUnread, t)
		}
	}
	s.staffRender(w, http.StatusOK, "widget", staffData{Title: "Client messages", V: v, Body: map[string]any{
		"Unread": unread, "List": listData{V: v, Threads: withUnread}}})
}

// widgetRows is how many conversations the widget lists.
const widgetRows = 8

// badge answers the sidebar badge (CORE-08): {count} of unread client messages on
// matters the viewer can read.
func (s *Server) badge(w http.ResponseWriter, r *http.Request, v *viewer) {
	n, err := s.unreadCount(r.Context(), v)
	if err != nil {
		s.fail(w, r, true, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"count": n})
}

func (s *Server) searchPage(w http.ResponseWriter, r *http.Request, v *viewer) {
	q := r.URL.Query().Get("q")
	ts, err := s.search(r.Context(), v, q)
	if err != nil {
		s.fail(w, r, false, err)
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeJSON(w, http.StatusOK, searchJSON(ts))
		return
	}
	s.staffRender(w, http.StatusOK, "search", staffData{Title: "Search", V: v, Body: map[string]any{"Q": q, "List": listData{V: v, Threads: ts}}})
}

// searchJSON is the hit list the core's search reads.
func searchJSON(ts []Thread) map[string]any {
	hits := make([]map[string]any, 0, len(ts))
	for _, t := range ts {
		title := t.Subject
		if title == "" {
			title = "Conversation with " + t.ContactName
		}
		hits = append(hits, map[string]any{"thread_id": t.ID, "matter_id": t.MatterID, "title": title,
			"subtitle": t.MatterLabel, "link": "/modules/" + ID + "/tabs/inbox?thread_id=" + url.QueryEscape(t.ID)})
	}
	return map[string]any{"data": hits}
}

// --- one thread ---

func (s *Server) staffThreadPage(w http.ResponseWriter, r *http.Request, v *viewer) {
	s.staffThreadView(w, r, v, r.PathValue("thread_id"), http.StatusOK, "", "")
}

func (s *Server) staffThreadView(w http.ResponseWriter, r *http.Request, v *viewer, id string, status int, note, errText string) {
	ctx := r.Context()
	t, ms, err := s.openThread(ctx, v, id)
	if err != nil {
		s.fail(w, r, false, err)
		return
	}
	set, err := s.settings(ctx)
	if err != nil {
		s.fail(w, r, false, err)
		return
	}
	fs, err := s.files(ctx, v, t.MatterID)
	if err != nil {
		s.fail(w, r, false, err)
		return
	}
	var attachable []File
	for _, f := range fs {
		if !f.AttorneyOnly && f.TrashedAt == nil && f.OnMatter(t.MatterID) {
			attachable = append(attachable, f)
		}
	}
	s.staffRender(w, status, "staff-thread", staffData{Title: "Messages", V: v, Notice: note, Error: errText,
		Body: map[string]any{"Thread": t, "Messages": ms, "Files": attachable, "Max": set.MessageMaxChars}})
}

// staffAfter redraws after a staff action: the thread with the result, or the refusal.
func (s *Server) staffAfter(w http.ResponseWriter, r *http.Request, v *viewer, threadID, note string, err error) {
	status, errText := http.StatusOK, ""
	if err != nil {
		e := asAPIError(err)
		if e.Status == http.StatusNotFound || e.Status >= http.StatusInternalServerError {
			s.fail(w, r, false, err)
			return
		}
		status, note, errText = e.Status, "", e.Message
	}
	if threadID == "" {
		if v.MatterID != "" {
			s.matterMessagesView(w, r, v, status, note, errText)
			return
		}
		renderMessage(w, status, errText)
		return
	}
	s.staffThreadView(w, r, v, threadID, status, note, errText)
}

func (s *Server) staffNewThreadPost(w http.ResponseWriter, r *http.Request, v *viewer) {
	_ = r.ParseForm()
	matter := r.PostFormValue("matter_id")
	if matter == "" {
		matter = v.MatterID
	}
	t, _, err := s.startThread(r.Context(), v, NewThread{MatterID: matter, ContactID: r.PostFormValue("contact_id"),
		Subject: r.PostFormValue("subject"), Body: r.PostFormValue("body"), FileIDs: r.PostForm["file_ids"]})
	s.staffAfter(w, r, v, t.ID, "Message sent.", err)
}

func (s *Server) staffReplyPost(w http.ResponseWriter, r *http.Request, v *viewer) {
	_ = r.ParseForm()
	id := r.PathValue("thread_id")
	_, err := s.reply(r.Context(), v, id, r.PostFormValue("body"), r.PostForm["file_ids"])
	s.staffAfter(w, r, v, id, "Message sent.", err)
}

func (s *Server) staffStatusPost(w http.ResponseWriter, r *http.Request, v *viewer) {
	id := r.PathValue("thread_id")
	_, err := s.setStatus(r.Context(), v, id, r.PostFormValue("status"))
	s.staffAfter(w, r, v, id, "Saved.", err)
}

func (s *Server) staffRetractPost(w http.ResponseWriter, r *http.Request, v *viewer) {
	id := r.PathValue("message_id")
	var thread string
	if validID(id) {
		_ = s.c.DB.QueryRowContext(r.Context(), `SELECT thread_id FROM messages WHERE id = $1`, id).Scan(&thread)
	}
	err := s.retract(r.Context(), v, id)
	if thread == "" {
		s.fail(w, r, false, errNotFound)
		return
	}
	s.staffAfter(w, r, v, thread, "Message retracted.", err)
}

// --- portal access panel ---

func (s *Server) accessPage(w http.ResponseWriter, r *http.Request, v *viewer) {
	s.accessView(w, r, v, http.StatusOK, nil, "")
}

func (s *Server) accessView(w http.ResponseWriter, r *http.Request, v *viewer, status int, res *AccessResult, errText string) {
	ctx := r.Context()
	m, err := s.accessMatter(ctx, v, "")
	if err != nil {
		s.fail(w, r, false, err)
		return
	}
	rows, err := s.accessRows(ctx, v, m)
	if err != nil {
		s.fail(w, r, false, err)
		return
	}
	body := map[string]any{"Rows": rows, "Sent": map[string]*AccessResult{}}
	note := ""
	if res != nil {
		switch res.Action {
		case "grant":
			body["Sent"] = map[string]*AccessResult{res.ContactID: res}
		case "temporary-password":
			body["Password"] = res
		case "deactivate":
			note = "Portal access is off."
		case "reactivate":
			note = "Portal access is on."
		}
	}
	s.staffRender(w, status, "access", staffData{Title: "Portal access", V: v, Notice: note, Error: errText, Body: body})
}

func (s *Server) accessPost(w http.ResponseWriter, r *http.Request, v *viewer) {
	m, err := s.accessMatter(r.Context(), v, "")
	if err != nil {
		s.fail(w, r, false, err)
		return
	}
	res, err := s.accessAction(r.Context(), v, m, r.PathValue("contact_id"), r.PathValue("action"))
	if err != nil {
		e := asAPIError(err)
		if e.Status == http.StatusNotFound || e.Status >= http.StatusInternalServerError {
			s.fail(w, r, false, err)
			return
		}
		s.accessView(w, r, v, e.Status, nil, e.Message)
		return
	}
	s.accessView(w, r, v, http.StatusOK, &res, "")
}

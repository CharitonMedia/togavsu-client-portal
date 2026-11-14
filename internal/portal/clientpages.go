package portal

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/CharitonMedia/togavsu-client-portal/internal/coreapi"
)

const clientTemplates = `
{{define "home"}}{{template "client-top" .}}
<h1>Dashboard</h1>
{{if .Body.Welcome}}<p class="welcome">{{.Body.Welcome}}</p>{{end}}
{{range .Body.Replenishments}}<div class="banner" data-replenishment="{{.ID}}"><p>The firm asks you to add {{money .AmountCents}} to your trust account.{{if .Message}} {{.Message}}{{end}}</p>
  {{if $.Body.DepositOK}}{{if $.Choice.Active}}<a class="button" target="_top" href="{{deposit $.Choice.Active.ID .ID}}">Make a deposit</a>{{end}}{{end}}</div>{{end}}
<section class="cards">
  <a class="card" href="{{link .V "/client/documents"}}"><h2>Documents</h2>{{if .Body.NewDocs}}<p><span class="dot" aria-hidden="true"></span> <span data-new-docs="{{.Body.NewDocs}}">{{.Body.NewDocs}} new</span></p>{{else}}<p>Nothing new</p>{{end}}</a>
  <a class="card" href="{{link .V "/client/messages"}}"><h2>Messages</h2><p data-unread="{{.Body.Unread}}">{{if .Body.Unread}}{{.Body.Unread}} unread{{else}}No unread messages{{end}}</p></a>
  <a class="card" href="{{link .V "/client/invoices"}}"><h2>Invoices</h2><p data-balance="{{.Body.BalanceDue}}">Balance due {{money .Body.BalanceDue}}</p></a>
  <div class="card"><h2>Trust balance</h2><p data-trust="{{.Body.Trust}}">{{money .Body.Trust}}</p>
    {{if .Body.DepositOK}}{{if .Choice.Active}}{{if not .Body.Replenishments}}<a class="button" target="_top" href="{{deposit .Choice.Active.ID ""}}">Make a deposit</a>{{end}}{{else}}<a class="button" href="{{link .V "/client/pick-matter" "next" "/client/home"}}">Make a deposit</a>{{end}}{{end}}</div>
  <div class="card"><h2>Next appointment</h2>{{with .Body.Next}}<p data-appointment="{{.Start}}">{{.Title}}<br>{{.Start}}{{if .Location}}<br>{{.Location}}{{end}}</p>{{else}}<p>None scheduled</p>{{end}}</div>
  {{if .Body.Awaiting}}<div class="card"><h2>Waiting for your signature</h2><ul>{{range .Body.Awaiting}}<li>{{.Name}} <a class="button" target="_top" href="{{sign .RequestID}}">Sign</a></li>{{end}}</ul></div>{{end}}
</section>
{{template "bottom"}}{{end}}

{{define "access-ended"}}{{template "head" .}}<body class="client" style="{{css .HeaderStyle}}"><main id="main">
<h1>Your portal access has ended</h1>
<p>Your matters with {{.Firm.Name}} are closed, so the portal no longer shows them. If you need a document or have a question, contact the office.</p>
<p class="firm-contact">{{.Firm.Name}}{{if .Firm.Phone}}<br>Phone: <span data-phone>{{.Firm.Phone}}</span>{{end}}{{if .Firm.Email}}<br>Email: <span data-email>{{.Firm.Email}}</span>{{end}}</p>
<form method="post" action="/portal/sign-out" target="_top"><button>Sign out</button></form>
{{template "bottom"}}{{end}}

{{define "pick"}}{{template "client-top" .}}
<h1>Choose a matter</h1>
<p>You have more than one matter with the firm. Choose the one this is about.</p>
<ul class="pick" data-picker>{{range .Choice.All}}<li><form method="post" action="{{link $.V "/client/active-matter"}}"><input type="hidden" name="next" value="{{$.Body.Next}}"><input type="hidden" name="matter_id" value="{{.ID}}"><button>{{.Label}}</button></form></li>{{end}}</ul>
{{template "bottom"}}{{end}}

{{define "documents"}}{{template "client-top" .}}
<h1>Documents</h1>
{{if .Choice.Active}}
<p class="matter">{{.Choice.Active.Label}}</p>
<form id="upload-form" data-matter="{{.Choice.Active.ID}}" data-upload="{{link .V "/client/upload"}}">
  <label>Upload documents <input type="file" name="files" multiple></label> <button>Upload</button>
  <ul id="upload-results" aria-live="polite"></ul>
</form>
{{if .Body.Docs}}<form method="post" action="{{link .V "/client/documents/archive"}}"><input type="hidden" name="matter_id" value="{{.Choice.Active.ID}}"><button data-download-all>Download all</button></form>{{end}}
<ul class="docs">{{range .Body.Docs}}<li data-file="{{.ID}}">{{if .New}}<span class="dot" aria-label="New"></span> {{end}}<span class="name">{{.Name}}</span>
  <a target="_blank" rel="noopener" href="{{link $.V (print "/client/documents/" .ID "/view")}}">View</a>
  {{with download .File "attachment"}}<a target="_blank" rel="noopener" href="{{.}}">Download</a>{{end}}
  {{range .Signatures}}<span class="sig" data-signature="{{.Status}}">{{if eq .Status "awaiting_client"}}<a class="button" target="_top" href="{{sign .SigningRequestID}}">Sign</a>{{else}}{{.Status}}{{end}}</span>{{end}}
</li>{{else}}<li>No documents yet.</li>{{end}}</ul>
{{else}}<p>Choose a matter to see its documents and upload files.</p><a class="button" href="{{link .V "/client/pick-matter" "next" "/client/documents"}}">Choose a matter</a>{{end}}
{{template "bottom"}}{{end}}

{{define "invoices"}}{{template "client-top" .}}
<h1>Invoices</h1>
{{with .Body.Detail}}<section class="invoice" data-invoice="{{.Invoice.ID}}">
  <h2>Invoice {{.Invoice.Number}}</h2>
  <p>Issued {{.Invoice.IssuedOn}} · Total {{money .Invoice.TotalCents}} · Paid {{money .Invoice.AmountPaidCents}} · Balance due {{money .Invoice.BalanceDueCents}}</p>
  {{if .Invoice.ClientMessage}}<p>{{.Invoice.ClientMessage}}</p>{{end}}
  <a target="_blank" rel="noopener" href="/api/v1/invoices/{{.Invoice.ID}}/pdf">PDF</a>
  {{if .Processing}}<p class="processing">Payment processing</p>{{else if .PayHref}}<a class="button pay" target="_top" href="{{.PayHref}}">Pay</a>{{end}}
</section>{{end}}
<table class="invoices"><thead><tr><th>Invoice</th><th>Issued</th><th>Status</th><th>Balance due</th><th></th></tr></thead><tbody>
{{range .Body.Rows}}<tr data-invoice="{{.Invoice.ID}}"><td><a href="{{link $.V "/client/invoices" "invoice_id" .Invoice.ID "matter_id" .Invoice.MatterID}}">{{.Invoice.Number}}</a></td><td>{{.Invoice.IssuedOn}}</td><td>{{.Invoice.Status}}</td><td>{{money .Invoice.BalanceDueCents}}</td>
<td>{{if .Processing}}<span class="processing">Payment processing</span>{{else if .PayHref}}<a class="button pay" {{if .Picker}}href="{{.PayHref}}"{{else}}target="_top" href="{{.PayHref}}"{{end}}>Pay</a>{{end}}</td></tr>
{{else}}<tr><td colspan="5">No invoices yet.</td></tr>{{end}}</tbody></table>
{{template "bottom"}}{{end}}

{{define "client-messages"}}{{template "client-top" .}}
<h1>Messages</h1>
{{with .Body.Thread}}<section class="thread" data-thread="{{.ID}}">
  <h2>{{if .Subject}}{{.Subject}}{{else}}Conversation{{end}}</h2><p class="matter">{{.MatterLabel}}</p>
  {{template "thread-messages" $.Body.Messages}}
  {{if eq .Status "closed"}}<p>This conversation is closed.</p>{{else}}
  <form method="post" action="{{link $.V (print "/client/messages/" .ID)}}" hx-post="{{link $.V (print "/client/messages/" .ID)}}" hx-target="body">
    <label>Reply<textarea name="body" required maxlength="{{$.Body.Max}}"></textarea></label>
    {{if $.Body.Files}}<fieldset><legend>Attach documents</legend>{{range $.Body.Files}}<label><input type="checkbox" name="file_ids" value="{{.ID}}"> {{.Name}}</label>{{end}}</fieldset>{{end}}
    <button>Send</button></form>{{end}}
</section>{{end}}
{{if .Choice.Active}}<details class="new"{{if not .Body.Threads}} open{{end}}><summary>New message</summary>
  <form method="post" action="{{link .V "/client/messages"}}" hx-post="{{link .V "/client/messages"}}" hx-target="body">
    <input type="hidden" name="matter_id" value="{{.Choice.Active.ID}}">
    <label>Subject<input name="subject" maxlength="200"></label>
    <label>Message<textarea name="body" required maxlength="{{.Body.Max}}"></textarea></label>
    {{if .Body.Files}}<fieldset><legend>Attach documents</legend>{{range .Body.Files}}<label><input type="checkbox" name="file_ids" value="{{.ID}}"> {{.Name}}</label>{{end}}</fieldset>{{end}}
    <button>Send</button></form></details>
{{else}}<a class="button" data-new-message href="{{link .V "/client/pick-matter" "next" "/client/messages"}}">New message</a>{{end}}
<ul class="threads">{{range .Body.Threads}}<li data-thread="{{.ID}}"><a href="{{link $.V "/client/messages" "thread_id" .ID "matter_id" .MatterID}}">{{if .Subject}}{{.Subject}}{{else}}Conversation{{end}}</a> <span class="matter">{{.MatterLabel}}</span>{{if .Unread}} <span class="unread">{{.Unread}} unread</span>{{end}}{{if eq .Status "closed"}} <span>Closed</span>{{end}}</li>{{else}}<li>No messages yet.</li>{{end}}</ul>
{{template "bottom"}}{{end}}

{{define "account"}}{{template "client-top" .}}
<h1>Account</h1>
<p><a target="_top" href="/portal/security">Change password</a></p>
<form method="post" action="{{link .V "/client/account"}}" hx-post="{{link .V "/client/account"}}" hx-target="body" class="contact">
  <h2>Your contact details</h2>
  <label>Email<input type="email" name="email" value="{{.Body.Contact.Email}}"></label>
  <label>Phone<input name="phone" value="{{.Body.Contact.Phone}}"></label>
  <label>Address<input name="line1" value="{{.Body.Contact.MailingAddress.Line1}}"></label>
  <label>Address line 2<input name="line2" value="{{.Body.Contact.MailingAddress.Line2}}"></label>
  <label>City<input name="city" value="{{.Body.Contact.MailingAddress.City}}"></label>
  <label>State<input name="region" value="{{.Body.Contact.MailingAddress.Region}}"></label>
  <label>ZIP<input name="postal_code" value="{{.Body.Contact.MailingAddress.PostalCode}}"></label>
  <label>Country<input name="country" value="{{.Body.Contact.MailingAddress.Country}}"></label>
  <button>Save</button>
</form>
{{if .Body.DepositOK}}{{if .Choice.Active}}<p><a class="button" target="_top" href="{{deposit .Choice.Active.ID ""}}">Make a deposit</a></p>{{end}}{{end}}
{{if .Body.Ledger}}<h2>Trust account activity</h2><table><thead><tr><th>Date</th><th>Type</th><th>Amount</th><th>Description</th></tr></thead><tbody>
{{range .Body.Ledger}}<tr><td>{{.Date}}</td><td>{{.Type}}</td><td>{{money .AmountCents}}</td><td>{{.Description}}</td></tr>{{end}}</tbody></table>{{end}}
{{template "bottom"}}{{end}}
`

// clientData is what every client page draws in its header and nav.
type clientData struct {
	Title, Tab, Path string
	V                *viewer
	Firm             FirmProfile
	Choice           Choice
	Nav              []NavTab
	Error, Notice    string
	Body             any
}

// HeaderStyle is the client header color from Settings > Appearance.
func (d clientData) HeaderStyle() string { return "--header: " + d.Firm.HeaderColor() }

// clientBase reads what every client page needs. ended is true when the client has no
// readable matter left: the page is then the access-ended page.
func (s *Server) clientBase(ctx context.Context, v *viewer, tab, path, named string) (clientData, bool, error) {
	c, err := s.choose(ctx, v, named)
	if err != nil {
		return clientData{}, false, err
	}
	d := clientData{Title: "Client Portal", Tab: tab, Path: path, V: v, Firm: s.firm(ctx, v), Choice: c}
	if len(c.All) == 0 {
		return d, true, nil
	}
	var nav []NavTab
	if err := s.api.Get(ctx, v.Token, "/api/v1/nav/client-context", &struct {
		Data *[]NavTab `json:"data"`
	}{&nav}); err == nil {
		for _, t := range nav {
			if t.ModuleID != ID {
				d.Nav = append(d.Nav, t)
			}
		}
	}
	return d, false, nil
}

// clientView runs a client page: the base, the access-ended page when nothing is
// readable, else body(d) to fill the page's own part.
func (s *Server) clientView(w http.ResponseWriter, r *http.Request, v *viewer, tab, name string, status int, note, errText string, body func(*clientData) error) {
	named := r.URL.Query().Get("matter_id")
	if r.Method == http.MethodPost {
		if m := r.PostFormValue("matter_id"); m != "" {
			named = m
		}
	}
	d, ended, err := s.clientBase(r.Context(), v, tab, "/client/"+tab, named)
	if err != nil {
		s.fail(w, r, false, err)
		return
	}
	if ended {
		render(w, http.StatusOK, "access-ended", d)
		return
	}
	d.Notice, d.Error = note, errText
	if err := body(&d); err != nil {
		s.fail(w, r, false, err)
		return
	}
	render(w, status, name, d)
}

func (s *Server) accessEndedPage(w http.ResponseWriter, r *http.Request, v *viewer) {
	d := clientData{Title: "Client Portal", V: v, Firm: s.firm(r.Context(), v)}
	render(w, http.StatusOK, "access-ended", d)
}

// --- dashboard ---

type awaiting struct {
	Name, RequestID string
}

type homeBody struct {
	Welcome        string
	NewDocs        int
	Unread         int
	BalanceDue     int64
	Trust          int64
	Replenishments []Replenishment
	DepositOK      bool
	Next           *Appointment
	Awaiting       []awaiting
}

func (s *Server) homePage(w http.ResponseWriter, r *http.Request, v *viewer) {
	s.clientView(w, r, v, "home", "home", http.StatusOK, "", "", func(d *clientData) error {
		ctx := r.Context()
		b := homeBody{}
		if set, err := s.settings(ctx); err == nil {
			b.Welcome = set.WelcomeText
		}
		scope := map[string]bool{}
		for _, m := range d.Choice.Scope() {
			scope[m.ID] = true
		}
		contact, err := s.myContact(ctx, v)
		if err != nil {
			return err
		}
		for _, m := range d.Choice.Scope() {
			docs, err := s.documents(ctx, v, m.ID)
			if err != nil {
				return err
			}
			var ids []string
			names := map[string]string{}
			for _, doc := range docs {
				if doc.New {
					b.NewDocs++
				}
				ids = append(ids, doc.ID)
				names[doc.ID] = doc.Name
			}
			for file, sigs := range s.signatures(ctx, v, contact, ids) {
				for _, sg := range sigs {
					if sg.Status == "awaiting_client" {
						b.Awaiting = append(b.Awaiting, awaiting{Name: names[file], RequestID: sg.SigningRequestID})
					}
				}
			}
			invs, err := s.invoices(ctx, v, m.ID)
			if err != nil {
				return err
			}
			for _, in := range invs {
				if in.Status == "sent" || in.Status == "viewed" {
					b.BalanceDue += in.BalanceDueCents
				}
			}
		}
		ts, err := s.listThreads(ctx, v, ThreadFilter{})
		if err != nil {
			return err
		}
		for _, t := range ts {
			if scope[t.MatterID] {
				b.Unread += t.Unread
			}
		}
		var trust struct {
			Data []TrustMatter `json:"data"`
		}
		if err := s.api.Get(ctx, v.Token, "/api/v1/me/trust", &trust); err == nil {
			for _, t := range trust.Data {
				if !scope[t.MatterID] {
					continue
				}
				b.Trust += t.BalanceCents
				for _, rq := range t.ReplenishmentRequests {
					if rq.Status == "active" {
						b.Replenishments = append(b.Replenishments, rq)
					}
				}
			}
		}
		var appts struct {
			Data []Appointment `json:"data"`
		}
		if err := s.api.Get(ctx, v.Token, "/api/v1/me/appointments", &appts); err == nil {
			sort.SliceStable(appts.Data, func(i, j int) bool { return appts.Data[i].Start < appts.Data[j].Start })
			for _, a := range appts.Data {
				if scope[a.MatterID] {
					a := a
					b.Next = &a
					break
				}
			}
		}
		if d.Choice.Active != nil {
			b.DepositOK = s.depositsEnabled(ctx, v, d.Choice.Active.ID)
		} else {
			for _, m := range d.Choice.All {
				if s.depositsEnabled(ctx, v, m.ID) {
					b.DepositOK = true
					break
				}
			}
		}
		d.Title, d.Body = "Dashboard", b
		return nil
	})
}

// --- matter picker ---

// safeNext keeps a picker's return path inside the module's client pages.
func safeNext(next string) string {
	u, err := url.Parse(next)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/client/") || strings.Contains(u.Path, "..") {
		return "/client/home"
	}
	return next
}

func (s *Server) pickMatterPage(w http.ResponseWriter, r *http.Request, v *viewer) {
	next := safeNext(r.URL.Query().Get("next"))
	s.clientView(w, r, v, "pick-matter", "pick", http.StatusOK, "", "", func(d *clientData) error {
		d.Title, d.Body = "Choose a matter", map[string]string{"Next": next}
		return nil
	})
}

func (s *Server) pickMatterPost(w http.ResponseWriter, r *http.Request, v *viewer) {
	matter := r.PostFormValue("matter_id")
	if err := s.saveActive(r.Context(), v, matter); err != nil {
		s.fail(w, r, false, err)
		return
	}
	next := safeNext(r.PostFormValue("next"))
	u, _ := url.Parse(next)
	q := u.Query()
	q.Set("context", v.Token)
	q.Set("matter_id", matter)
	http.Redirect(w, r, "/m/"+ID+u.Path+"?"+q.Encode(), http.StatusSeeOther)
}

// --- documents ---

func (s *Server) documentsPage(w http.ResponseWriter, r *http.Request, v *viewer) {
	s.clientView(w, r, v, "documents", "documents", http.StatusOK, "", "", func(d *clientData) error {
		d.Title = "Documents"
		body := map[string]any{}
		if d.Choice.Active != nil {
			docs, err := s.documents(r.Context(), v, d.Choice.Active.ID)
			if err != nil {
				return err
			}
			contact, err := s.myContact(r.Context(), v)
			if err != nil {
				return err
			}
			ids := make([]string, 0, len(docs))
			for _, doc := range docs {
				ids = append(ids, doc.ID)
			}
			sigs := s.signatures(r.Context(), v, contact, ids)
			for i := range docs {
				docs[i].Signatures = sigs[docs[i].ID]
			}
			body["Docs"] = docs
		}
		d.Body = body
		return nil
	})
}

// --- invoices ---

// invoices lists the invoices the core shows the client on the matter; drafts and
// voids never show.
func (s *Server) invoices(ctx context.Context, v *viewer, matter string) ([]Invoice, error) {
	var page struct {
		Data []Invoice `json:"data"`
	}
	if err := s.api.Get(ctx, v.Token, "/api/v1/invoices?limit=100&matter_id="+url.QueryEscape(matter), &page); err != nil {
		return nil, err
	}
	var out []Invoice
	for _, in := range page.Data {
		if in.Status == "sent" || in.Status == "viewed" || in.Status == "paid" {
			out = append(out, in)
		}
	}
	return out, nil
}

type invoiceRow struct {
	Invoice    Invoice
	PayHref    string
	Picker     bool
	Processing bool
}

func (s *Server) invoicesPage(w http.ResponseWriter, r *http.Request, v *viewer) {
	invoiceID := r.URL.Query().Get("invoice_id")
	s.clientView(w, r, v, "invoices", "invoices", http.StatusOK, "", "", func(d *clientData) error {
		ctx := r.Context()
		d.Title = "Invoices"
		var all []Invoice
		for _, m := range d.Choice.Scope() {
			invs, err := s.invoices(ctx, v, m.ID)
			if err != nil {
				return err
			}
			all = append(all, invs...)
		}
		var detail *Invoice
		if invoiceID != "" {
			if !validID(invoiceID) {
				return errNotFound
			}
			var in Invoice
			// Opening an invoice is the core's viewed transition.
			if err := s.api.Get(ctx, v.Token, "/api/v1/invoices/"+invoiceID, &in); err != nil {
				return err
			}
			if in.Status != "sent" && in.Status != "viewed" && in.Status != "paid" {
				return errNotFound
			}
			if _, ok := d.Choice.has(in.MatterID); !ok {
				return errNotFound
			}
			detail = &in
			for i := range all {
				if all[i].ID == in.ID {
					all[i] = in
				}
			}
		}
		ids := make([]string, 0, len(all))
		for _, in := range all {
			ids = append(ids, in.ID)
		}
		if detail != nil && len(ids) == 0 {
			ids = append(ids, detail.ID)
		}
		pay := s.invoicePayability(ctx, v, ids)
		row := func(in Invoice) invoiceRow {
			p := pay[in.ID]
			rw := invoiceRow{Invoice: in, Processing: p.UnpayableReason == "in_progress"}
			if p.Payable && !rw.Processing {
				if d.Choice.Active == nil {
					rw.PayHref, rw.Picker = link(v, "/client/pick-matter", "next", "/client/invoices"), true
				} else {
					rw.PayHref = payLink(in.ID)
				}
			}
			return rw
		}
		body := map[string]any{}
		var rows []invoiceRow
		for _, in := range all {
			rows = append(rows, row(in))
		}
		body["Rows"] = rows
		if detail != nil {
			rw := row(*detail)
			body["Detail"] = &rw
		}
		d.Body = body
		return nil
	})
}

// --- messages ---

func (s *Server) clientMessagesData(ctx context.Context, v *viewer, d *clientData, threadID string) error {
	set, err := s.settings(ctx)
	if err != nil {
		return err
	}
	body := map[string]any{"Max": set.MessageMaxChars}
	ts, err := s.listThreads(ctx, v, ThreadFilter{})
	if err != nil {
		return err
	}
	body["Threads"] = ts
	if threadID != "" {
		t, ms, err := s.openThread(ctx, v, threadID)
		if err != nil {
			return err
		}
		body["Thread"], body["Messages"] = t, ms
		docs, err := s.documents(ctx, v, t.MatterID)
		if err != nil {
			return err
		}
		body["Files"] = docs
	} else if d.Choice.Active != nil {
		docs, err := s.documents(ctx, v, d.Choice.Active.ID)
		if err != nil {
			return err
		}
		body["Files"] = docs
	}
	d.Title, d.Body = "Messages", body
	return nil
}

func (s *Server) clientMessagesPage(w http.ResponseWriter, r *http.Request, v *viewer) {
	s.clientView(w, r, v, "messages", "client-messages", http.StatusOK, "", "", func(d *clientData) error {
		return s.clientMessagesData(r.Context(), v, d, r.URL.Query().Get("thread_id"))
	})
}

func (s *Server) clientNewThreadPost(w http.ResponseWriter, r *http.Request, v *viewer) {
	_ = r.ParseForm()
	c, err := s.choose(r.Context(), v, r.PostFormValue("matter_id"))
	if err != nil {
		s.fail(w, r, false, err)
		return
	}
	if c.Active == nil {
		http.Redirect(w, r, link(v, "/client/pick-matter", "next", "/client/messages"), http.StatusSeeOther)
		return
	}
	t, _, err := s.startThread(r.Context(), v, NewThread{MatterID: c.Active.ID, Subject: r.PostFormValue("subject"),
		Body: r.PostFormValue("body"), FileIDs: r.PostForm["file_ids"]})
	s.afterClientPost(w, r, v, t.ID, err)
}

func (s *Server) clientReplyPost(w http.ResponseWriter, r *http.Request, v *viewer) {
	_ = r.ParseForm()
	id := r.PathValue("thread_id")
	_, err := s.reply(r.Context(), v, id, r.PostFormValue("body"), r.PostForm["file_ids"])
	s.afterClientPost(w, r, v, id, err)
}

// afterClientPost redraws the messages page with the thread open, or with the refusal
// on screen.
func (s *Server) afterClientPost(w http.ResponseWriter, r *http.Request, v *viewer, threadID string, err error) {
	status, note, errText := http.StatusOK, "Message sent.", ""
	if err != nil {
		e := asAPIError(err)
		if e.Status == http.StatusNotFound || e.Status >= http.StatusInternalServerError {
			s.fail(w, r, false, err)
			return
		}
		status, note, errText = e.Status, "", e.Message
	}
	s.clientView(w, r, v, "messages", "client-messages", status, note, errText, func(d *clientData) error {
		return s.clientMessagesData(r.Context(), v, d, threadID)
	})
}

// --- account ---

func (s *Server) accountData(ctx context.Context, v *viewer, d *clientData) error {
	var c MyContact
	if err := s.api.Get(ctx, v.Token, "/api/v1/me/contact", &c); err != nil {
		return err
	}
	body := map[string]any{"Contact": c}
	if d.Choice.Active != nil {
		body["DepositOK"] = s.depositsEnabled(ctx, v, d.Choice.Active.ID)
		var ledger struct {
			Data []TrustEntry `json:"data"`
		}
		if err := s.api.Get(ctx, v.Token, "/api/v1/me/trust-ledger?matter_id="+url.QueryEscape(d.Choice.Active.ID), &ledger); err == nil {
			body["Ledger"] = ledger.Data
		}
	}
	d.Title, d.Body = "Account", body
	return nil
}

func (s *Server) accountPage(w http.ResponseWriter, r *http.Request, v *viewer) {
	s.clientView(w, r, v, "account", "account", http.StatusOK, "", "", func(d *clientData) error {
		return s.accountData(r.Context(), v, d)
	})
}

// accountPost saves the client's own contact details through the core (CORE-13), which
// keeps the old row with an end date and records the client as the actor.
func (s *Server) accountPost(w http.ResponseWriter, r *http.Request, v *viewer) {
	c := MyContact{Email: strings.TrimSpace(r.PostFormValue("email")), Phone: strings.TrimSpace(r.PostFormValue("phone")),
		MailingAddress: Address{Line1: r.PostFormValue("line1"), Line2: r.PostFormValue("line2"), City: r.PostFormValue("city"),
			Region: r.PostFormValue("region"), PostalCode: r.PostFormValue("postal_code"), Country: r.PostFormValue("country")}}
	status, note, errText := http.StatusOK, "Saved.", ""
	if err := s.api.Patch(r.Context(), v.Token, "/api/v1/me/contact", c, nil); err != nil {
		if st := coreapi.StatusOf(err); st != http.StatusBadRequest && st != http.StatusUnprocessableEntity {
			s.fail(w, r, false, err)
			return
		}
		status, note, errText = http.StatusBadRequest, "", "Those details couldn't be saved. Check the email address and try again."
	}
	s.clientView(w, r, v, "account", "account", status, note, errText, func(d *clientData) error {
		return s.accountData(r.Context(), v, d)
	})
}

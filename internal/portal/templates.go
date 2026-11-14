package portal

import (
	"bytes"
	"encoding/json"
	"html/template"
	"net/http"
	"time"
)

// Pages are server-rendered HTML with HTMX, phone width first. Every link and form in
// a page carries the viewer's context token (hx-vals on the body for HTMX requests,
// ?context= on links). The page script (static/portal.js) refreshes an expired token
// from the parent frame (togavsu:context.refresh, CORE-09) and retries once.

var funcs = template.FuncMap{
	"money": money,
	"link":  link,
	"vals": func(token string) string {
		b, _ := json.Marshal(map[string]string{"context": token})
		return string(b)
	},
	"when": func(unix int64) string {
		if unix == 0 {
			return ""
		}
		return time.Unix(unix, 0).UTC().Format("Jan 2, 2006 3:04 PM") + " UTC"
	},
	"css": func(s string) template.CSS { return template.CSS(s) },
	"pay":     payLink,
	"deposit": depositLink,
	"sign":    signLink,
	"download": downloadPath,
	"portal": func(module, tab string) string { return portalPath(module, tab) },
}

const layouts = `
{{define "head"}}<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<link rel="stylesheet" href="/m/client-portal/static/portal.css">
<script src="/m/client-portal/static/htmx.min.js"></script>
<script src="/m/client-portal/static/portal.js"></script>
</head>{{end}}

{{define "client-top"}}{{template "head" .}}
<body class="client" data-context="{{.V.Token}}" hx-vals="{{vals .V.Token}}" style="{{css .HeaderStyle}}">
<header class="top">
  <div class="brand"><strong>{{.Firm.Short}}</strong> <span>{{.Firm.PortalLabel}}</span></div>
  {{if gt (len .Choice.All) 1}}<form class="picker" method="post" action="{{link .V "/client/active-matter"}}">
    <input type="hidden" name="next" value="{{.Path}}">
    <label>Matter <select name="matter_id" onchange="this.form.submit()">
      {{if not .Choice.Active}}<option value="">Choose a matter</option>{{end}}
      {{range .Choice.All}}<option value="{{.ID}}"{{if $.Choice.Active}}{{if eq .ID $.Choice.Active.ID}} selected{{end}}{{end}}>{{.Label}}</option>{{end}}
    </select></label><noscript><button>Go</button></noscript></form>{{end}}
  <form method="post" action="/portal/sign-out" target="_top" class="signout"><button>Sign out</button></form>
</header>
<nav class="tabs">
  <a href="{{link .V "/client/home"}}"{{if eq .Tab "home"}} aria-current="page"{{end}}>Dashboard</a>
  <a href="{{link .V "/client/documents"}}"{{if eq .Tab "documents"}} aria-current="page"{{end}}>Documents</a>
  <a href="{{link .V "/client/invoices"}}"{{if eq .Tab "invoices"}} aria-current="page"{{end}}>Invoices</a>
  <a href="{{link .V "/client/messages"}}"{{if eq .Tab "messages"}} aria-current="page"{{end}}>Messages</a>
  <a href="{{link .V "/client/account"}}"{{if eq .Tab "account"}} aria-current="page"{{end}}>Account</a>
  {{range .Nav}}<a href="{{portal .ModuleID .TabID}}" target="_top">{{.Title}}</a>{{end}}
</nav>
<main id="main">
{{if .Error}}<p class="error" role="alert">{{.Error}}</p>{{end}}
{{if .Notice}}<p class="notice" role="status">{{.Notice}}</p>{{end}}
{{end}}

{{define "staff-top"}}{{template "head" .}}
<body class="staff" data-context="{{.V.Token}}" hx-vals="{{vals .V.Token}}">
<main id="main">
{{if .Error}}<p class="error" role="alert">{{.Error}}</p>{{end}}
{{if .Notice}}<p class="notice" role="status">{{.Notice}}</p>{{end}}
{{end}}

{{define "bottom"}}</main></body></html>{{end}}

{{define "message"}}<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Client Portal</title>
<link rel="stylesheet" href="/m/client-portal/static/portal.css"></head><body><main><p class="error" role="alert">{{.}}</p></main></body></html>{{end}}

{{define "expired"}}<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Client Portal</title>
<link rel="stylesheet" href="/m/client-portal/static/portal.css">
<script src="/m/client-portal/static/portal.js"></script></head>
<body data-expired="1"><main><p id="expired-note">Refreshing your session...</p>
<p id="expired-signin" hidden>Your session ended. <a href="/login" target="_top">Sign in again</a>.</p></main></body></html>{{end}}

{{define "thread-messages"}}<ol class="messages">
{{range .}}<li class="msg {{.AuthorKind}}" id="msg-{{.ID}}">
  <div class="meta"><strong>{{.AuthorName}}</strong> <time>{{when .CreatedAt}}</time>{{if .ReadAt}} <span class="read">Read</span>{{end}}</div>
  {{if .Removed}}<p class="removed">Message removed</p>{{else}}<p class="body">{{.Body}}</p>
  {{if .Attachments}}<ul class="attachments">{{range .Attachments}}<li data-file="{{.FileID}}">{{.Name}}</li>{{end}}</ul>{{end}}{{end}}
</li>{{end}}
</ol>{{end}}
`

var pages = template.Must(template.New("pages").Funcs(funcs).Parse(layouts + clientTemplates + staffTemplates))

// render writes a page with the module's response headers.
func render(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := pages.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, "the page could not be drawn", http.StatusInternalServerError)
		return
	}
	pageHeaders(w)
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func pageHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; form-action 'self'; frame-ancestors 'self'; base-uri 'none'")
}

func renderMessage(w http.ResponseWriter, status int, msg string) {
	render(w, status, "message", msg)
}

// renderExpired answers a page request whose context token is missing, forged,
// expired or issued for another module: 401 with the script that asks the parent frame
// for a fresh token and reloads once.
func (s *Server) renderExpired(w http.ResponseWriter, _ *http.Request) {
	render(w, http.StatusUnauthorized, "expired", nil)
}

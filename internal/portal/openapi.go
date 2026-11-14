package portal

import (
	"net/http"
	"strings"
)

// endpoint is one JSON endpoint listed in the module's OpenAPI document.
type endpoint struct {
	Method, Path, ID, Who, Summary string
}

// Endpoints is every JSON endpoint under /m/client-portal/api/. Each page action has
// one, so an agent can do anything a person can do on the module's screens.
var Endpoints = []endpoint{
	{"get", "/api/threads", "listThreads", "staff, client", "Threads the viewer may see, newest message first. Filters: matter_id, contact_id, status (active, archived, closed, all)."},
	{"post", "/api/threads", "startThread", "staff, client", "Start a thread {matter_id, contact_id, subject, body, file_ids}. A client's contact_id is always their own."},
	{"get", "/api/threads/{thread_id}", "getThread", "staff, client", "A thread with its messages; stamps the other side's messages read."},
	{"post", "/api/threads/{thread_id}/messages", "replyThread", "staff, client", "Reply {body, file_ids}. A closed thread answers 409."},
	{"post", "/api/threads/{thread_id}/status", "setThreadStatus", "staff", "Set status {status: active, archived or closed}."},
	{"post", "/api/messages/{message_id}/retract", "retractMessage", "staff author", "Retract the author's own message."},
	{"get", "/api/unread", "unreadCount", "staff", "{count} of unread client messages on matters the viewer sees."},
	{"get", "/api/search", "searchThreads", "staff", "Thread hits for q."},
	{"post", "/api/active-matter", "setActiveMatter", "client", "Save the active matter {matter_id}."},
	{"post", "/api/uploads", "createUpload", "client", "Start an upload {matter_id, file_name, size_bytes}; send chunks with PUT and Content-Range."},
	{"put", "/api/uploads/{upload_id}", "putUploadChunk", "client", "One chunk, Content-Range: bytes start-end/total, in order."},
	{"post", "/api/uploads/{upload_id}/complete", "completeUpload", "client", "Finish the upload; it becomes a core file shared with the client."},
	{"post", "/api/archives", "downloadArchive", "client", "Zip of every document the client sees on {matter_id}."},
	{"get", "/api/access", "listPortalAccess", "staff", "Portal status of each client party on matter_id."},
	{"post", "/api/access/{contact_id}/{action}", "portalAccessAction", "staff", "action is grant, temporary-password, deactivate or reactivate; body {matter_id}."},
}

func (s *Server) openapi(w http.ResponseWriter, _ *http.Request) {
	paths := map[string]map[string]any{}
	for _, e := range Endpoints {
		if paths[e.Path] == nil {
			paths[e.Path] = map[string]any{}
		}
		var params []map[string]any
		for _, seg := range strings.Split(e.Path, "/") {
			if strings.HasPrefix(seg, "{") {
				params = append(params, map[string]any{"name": strings.Trim(seg, "{}"), "in": "path", "required": true, "schema": map[string]string{"type": "string"}})
			}
		}
		op := map[string]any{"operationId": e.ID, "summary": e.Summary, "x-togavsu-viewers": e.Who,
			"security": []map[string][]string{{"contextToken": {}}},
			"responses": map[string]any{"200": map[string]string{"description": "OK"}, "401": map[string]string{"description": "The context token is invalid or expired."},
				"404": map[string]string{"description": "Not found, or not for this viewer."}}}
		if params != nil {
			op["parameters"] = params
		}
		paths[e.Path][e.Method] = op
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"openapi": "3.1.0",
		"info":    map[string]string{"title": "Client Portal module API", "version": "1.0.0"},
		"servers": []map[string]string{{"url": "/m/" + ID}},
		"paths":   paths,
		"components": map[string]any{"securitySchemes": map[string]any{"contextToken": map[string]string{
			"type": "apiKey", "in": "header", "name": "X-Togavsu-Context-Token",
			"description": "A context token from POST /api/v1/modules/client-portal/context-tokens. It may also be sent as the context query parameter."}}},
	})
}

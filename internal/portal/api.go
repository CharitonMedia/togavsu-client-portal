package portal

import (
	"net/http"
)

// JSON endpoints for the thread actions. Each does exactly what the page action does.

func (s *Server) apiListThreads(w http.ResponseWriter, r *http.Request, v *viewer) {
	ts, err := s.listThreads(r.Context(), v, filterFrom(r))
	if err != nil {
		s.fail(w, r, true, err)
		return
	}
	if ts == nil {
		ts = []Thread{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": ts})
}

func (s *Server) apiStartThread(w http.ResponseWriter, r *http.Request, v *viewer) {
	var in NewThread
	if e := decodeJSON(r, &in); e != nil {
		writeJSONError(w, e)
		return
	}
	if v.isClient() && in.MatterID == "" {
		c, err := s.choose(r.Context(), v, "")
		if err != nil {
			s.fail(w, r, true, err)
			return
		}
		if c.Active != nil {
			in.MatterID = c.Active.ID
		}
	}
	t, m, err := s.startThread(r.Context(), v, in)
	if err != nil {
		s.fail(w, r, true, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"thread": t, "message": m})
}

func (s *Server) apiGetThread(w http.ResponseWriter, r *http.Request, v *viewer) {
	t, ms, err := s.openThread(r.Context(), v, r.PathValue("thread_id"))
	if err != nil {
		s.fail(w, r, true, err)
		return
	}
	if ms == nil {
		ms = []Message{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"thread": t, "messages": ms})
}

func (s *Server) apiReply(w http.ResponseWriter, r *http.Request, v *viewer) {
	var in struct {
		Body    string   `json:"body"`
		FileIDs []string `json:"file_ids"`
	}
	if e := decodeJSON(r, &in); e != nil {
		writeJSONError(w, e)
		return
	}
	m, err := s.reply(r.Context(), v, r.PathValue("thread_id"), in.Body, in.FileIDs)
	if err != nil {
		s.fail(w, r, true, err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) apiThreadStatus(w http.ResponseWriter, r *http.Request, v *viewer) {
	var in struct {
		Status string `json:"status"`
	}
	if e := decodeJSON(r, &in); e != nil {
		writeJSONError(w, e)
		return
	}
	t, err := s.setStatus(r.Context(), v, r.PathValue("thread_id"), in.Status)
	if err != nil {
		s.fail(w, r, true, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) apiRetract(w http.ResponseWriter, r *http.Request, v *viewer) {
	if err := s.retract(r.Context(), v, r.PathValue("message_id")); err != nil {
		s.fail(w, r, true, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": r.PathValue("message_id"), "removed": true})
}

func (s *Server) apiUnread(w http.ResponseWriter, r *http.Request, v *viewer) {
	s.badge(w, r, v)
}

func (s *Server) apiSearch(w http.ResponseWriter, r *http.Request, v *viewer) {
	ts, err := s.search(r.Context(), v, r.URL.Query().Get("q"))
	if err != nil {
		s.fail(w, r, true, err)
		return
	}
	writeJSON(w, http.StatusOK, searchJSON(ts))
}

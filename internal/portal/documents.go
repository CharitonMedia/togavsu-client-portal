package portal

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
)

// --- the client's matters and the active matter ---

// Choice is the client's readable matters and the matter the pages work on. Active is
// nil when the client has several matters and hasn't chosen one: write paths then show
// the matter picker and never guess.
type Choice struct {
	All    []Matter
	Active *Matter
}

func (c Choice) has(id string) (Matter, bool) {
	for _, m := range c.All {
		if m.ID == id {
			return m, true
		}
	}
	return Matter{}, false
}

// Scope is the active matter, or every readable matter when none is chosen.
func (c Choice) Scope() []Matter {
	if c.Active != nil {
		return []Matter{*c.Active}
	}
	return c.All
}

// choose resolves the active matter: a named matter the client can read (404 when
// they can't), else their only matter, else their saved choice while still readable.
func (s *Server) choose(ctx context.Context, v *viewer, named string) (Choice, error) {
	all, err := s.listMatters(ctx, v, "")
	if err != nil {
		return Choice{}, err
	}
	c := Choice{All: all}
	if named != "" {
		m, ok := c.has(named)
		if !ok {
			return Choice{}, errNotFound
		}
		c.Active = &m
		return c, nil
	}
	if len(all) == 1 {
		c.Active = &all[0]
		return c, nil
	}
	var saved sql.NullString
	err = s.c.DB.QueryRowContext(ctx, `SELECT active_matter_id FROM portal_preferences WHERE user_id = $1`, v.UserID).Scan(&saved)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Choice{}, err
	}
	if saved.Valid {
		if m, ok := c.has(saved.String); ok {
			c.Active = &m
		}
	}
	return c, nil
}

// saveActive remembers the client's choice for the next sign-in.
func (s *Server) saveActive(ctx context.Context, v *viewer, matter string) error {
	c, err := s.choose(ctx, v, matter)
	if err != nil {
		return err
	}
	_, err = s.c.DB.ExecContext(ctx, `INSERT INTO portal_preferences (user_id, active_matter_id, updated_at) VALUES ($1, $2, $3)
  ON CONFLICT (user_id) DO UPDATE SET active_matter_id = EXCLUDED.active_matter_id, updated_at = EXCLUDED.updated_at`, v.UserID, c.Active.ID, s.now())
	return err
}

func (s *Server) apiActiveMatter(w http.ResponseWriter, r *http.Request, v *viewer) {
	var in struct {
		MatterID string `json:"matter_id"`
	}
	if e := decodeJSON(r, &in); e != nil {
		writeJSONError(w, e)
		return
	}
	if err := s.saveActive(r.Context(), v, in.MatterID); err != nil {
		s.fail(w, r, true, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"active_matter_id": in.MatterID})
}

// --- documents ---

// Document is a shared document as the client sees it.
type Document struct {
	File
	New        bool
	Signatures []SignatureRequest
}

// documents lists the files the core shows the client on the matter, keeping only
// shared, live, non-attorney-only ones, newest first, each marked new until viewed.
func (s *Server) documents(ctx context.Context, v *viewer, matter string) ([]Document, error) {
	fs, err := s.files(ctx, v, matter)
	if err != nil {
		return nil, err
	}
	viewed, err := s.viewed(ctx, v.UserID)
	if err != nil {
		return nil, err
	}
	var out []Document
	for _, f := range fs {
		if !f.SharedWithClient || f.AttorneyOnly || f.TrashedAt != nil || !f.OnMatter(matter) {
			continue
		}
		out = append(out, Document{File: f, New: !viewed[f.ID]})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

func (s *Server) viewed(ctx context.Context, user string) (map[string]bool, error) {
	rows, err := s.c.DB.QueryContext(ctx, `SELECT file_id FROM file_views WHERE user_id = $1`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// recordView notes an inline view, which clears the "new" dot. A download doesn't.
func (s *Server) recordView(ctx context.Context, v *viewer, file string) error {
	now := s.now()
	_, err := s.c.DB.ExecContext(ctx, `INSERT INTO file_views (file_id, user_id, first_viewed_at, last_viewed_at) VALUES ($1, $2, $3, $3)
  ON CONFLICT (file_id, user_id) DO UPDATE SET last_viewed_at = EXCLUDED.last_viewed_at`, file, v.UserID, now)
	return err
}

// downloadPath is the core's download address of a file's current version, opened by
// the browser with the client's own core session.
func downloadPath(f File, disposition string) string {
	if f.CurrentVersionID == nil {
		return ""
	}
	return "/api/v1/files/" + f.ID + "/versions/" + *f.CurrentVersionID + "/download?disposition=" + disposition
}

// viewDocument records the inline view, then sends the browser to the core's inline
// download.
func (s *Server) viewDocument(w http.ResponseWriter, r *http.Request, v *viewer) {
	f, err := s.file(r.Context(), v, r.PathValue("file_id"))
	if err != nil {
		s.fail(w, r, false, err)
		return
	}
	if f.MatterID == nil || !f.SharedWithClient || f.AttorneyOnly || f.TrashedAt != nil || f.CurrentVersionID == nil {
		http.NotFound(w, r)
		return
	}
	if ok, err := s.canRead(r.Context(), v, *f.MatterID); err != nil || !ok {
		s.fail(w, r, false, errNotFound)
		return
	}
	if err := s.recordView(r.Context(), v, f.ID); err != nil {
		s.fail(w, r, false, err)
		return
	}
	http.Redirect(w, r, downloadPath(f, "inline"), http.StatusSeeOther)
}

// archive relays the core's zip of every document the client sees on the matter, from
// the same list the documents page shows, streamed through without being kept.
func (s *Server) archive(ctx context.Context, w http.ResponseWriter, v *viewer, matter string) error {
	c, err := s.choose(ctx, v, matter)
	if err != nil {
		return err
	}
	if c.Active == nil {
		return &apiError{Status: http.StatusBadRequest, Code: "matter_required", Message: "Choose a matter first."}
	}
	docs, err := s.documents(ctx, v, c.Active.ID)
	if err != nil {
		return err
	}
	if len(docs) == 0 {
		return badRequest("There are no documents to download yet.")
	}
	ids := make([]string, 0, len(docs))
	for _, d := range docs {
		ids = append(ids, d.ID)
	}
	resp, err := s.api.Raw(ctx, requestJSON(http.MethodPost, "/api/v1/file-archives", v.Token, map[string]any{"file_ids": ids}))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	name := strings.NewReplacer(`"`, "", "/", "-", `\`, "-").Replace(c.Active.Number)
	if name == "" {
		name = "documents"
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`-documents.zip"`)
	w.WriteHeader(http.StatusOK)
	_, err = io.Copy(w, resp.Body)
	return err
}

func (s *Server) apiArchive(w http.ResponseWriter, r *http.Request, v *viewer) {
	var in struct {
		MatterID string `json:"matter_id"`
	}
	if e := decodeJSON(r, &in); e != nil {
		writeJSONError(w, e)
		return
	}
	if in.MatterID == "" {
		writeJSONError(w, &apiError{Status: http.StatusBadRequest, Code: "matter_required", Message: "Choose a matter first."})
		return
	}
	if err := s.archive(r.Context(), w, v, in.MatterID); err != nil {
		s.fail(w, r, true, err)
	}
}

func (s *Server) archivePost(w http.ResponseWriter, r *http.Request, v *viewer) {
	if err := s.archive(r.Context(), w, v, r.PostFormValue("matter_id")); err != nil {
		s.fail(w, r, false, err)
	}
}

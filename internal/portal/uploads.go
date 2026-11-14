package portal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/CharitonMedia/togavsu-client-portal/internal/coreapi"
)

// Client uploads, rule for rule from Unified: streamed to the core's chunked upload
// API; at most upload_max_file_mb per file; at most upload_daily_files files and
// upload_daily_mb per matter in 24 hours; at most upload_rate_per_5min uploads per
// client in a fixed 5-minute window; a content-sniffed type check with the xlsx ZIP
// exception; empty files refused. A refused upload never reaches publishFile, so it
// leaves no file record. The core makes a published client upload source
// client_upload, origin received (CORE-16).

// Upload messages the client sees.
const (
	msgWrongType = "That file has to be a PDF, Word, Excel, text, or image file."
	msgEmpty     = "That file was empty."
	msgDaily     = "That's as much as this portal accepts in a day. Call the office and we'll take the rest another way."
	msgRate      = "That's a lot of uploads at once. Wait a few minutes and try again."
)

// Fixed numbers of the rule itself, not tunables: the 24-hour and 5-minute windows,
// a megabyte, and how much of a file the type check reads.
const (
	dayWindowSeconds  = 24 * 60 * 60
	rateWindowSeconds = 5 * 60
	megabyte          = 1 << 20
	sniffBytes        = 64 << 10
)

// FileType is what the type check decided.
type FileType struct {
	Kind      string
	MediaType string
}

var errWrongType = errors.New(msgWrongType)

// SniffType decides from a file's first bytes and its name whether a client may upload
// it. PDF, JPEG, PNG, HEIC and DOCX are recognized by content; an XLSX is a ZIP with the
// .xlsx extension (the ZIP exception); DOC and XLS are OLE files with their extension;
// TXT and CSV are text with their extension. Anything else, an executable renamed to an
// allowed extension included, is refused.
func SniffType(name string, head []byte) (FileType, error) {
	if len(head) == 0 {
		return FileType{}, errors.New(msgEmpty)
	}
	ext := strings.ToLower(path.Ext(name))
	has := func(prefix string) bool { return bytes.HasPrefix(head, []byte(prefix)) }
	switch {
	case has("%PDF-"):
		return FileType{"pdf", "application/pdf"}, nil
	case has("\xff\xd8\xff"):
		return FileType{"jpeg", "image/jpeg"}, nil
	case has("\x89PNG\r\n\x1a\n"):
		return FileType{"png", "image/png"}, nil
	case len(head) >= 12 && string(head[4:8]) == "ftyp" && heicBrand(string(head[8:12])):
		return FileType{"heic", "image/heic"}, nil
	case has("PK\x03\x04"):
		switch {
		case ext == ".docx" && bytes.Contains(head, []byte("word/")):
			return FileType{"docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"}, nil
		case ext == ".xlsx":
			return FileType{"xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"}, nil
		}
		return FileType{}, errWrongType
	case has("\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1"):
		switch ext {
		case ".doc":
			return FileType{"doc", "application/msword"}, nil
		case ".xls":
			return FileType{"xls", "application/vnd.ms-excel"}, nil
		}
		return FileType{}, errWrongType
	}
	if (ext == ".txt" || ext == ".csv") && looksText(head) {
		if ext == ".csv" {
			return FileType{"csv", "text/csv"}, nil
		}
		return FileType{"txt", "text/plain"}, nil
	}
	return FileType{}, errWrongType
}

func heicBrand(b string) bool {
	switch b {
	case "heic", "heix", "heim", "heis", "hevc", "hevx", "mif1", "msf1":
		return true
	}
	return false
}

// looksText is text with no NUL byte and no executable header.
func looksText(b []byte) bool {
	if bytes.HasPrefix(b, []byte("MZ")) || bytes.HasPrefix(b, []byte("\x7fELF")) {
		return false
	}
	return bytes.IndexByte(b, 0) < 0
}

// WindowStep is the fixed-window limiter: a window opens at the first upload and
// lasts window seconds; within it at most limit uploads pass. It returns the new
// window and whether this upload passes, and when refused, the seconds until the window
// ends.
func WindowStep(start int64, count int64, now int64, limit int64, window int64) (newStart, newCount int64, ok bool, retryAfter int64) {
	if now-start >= window || now < start {
		start, count = now, 0
	}
	if count >= limit {
		return start, count, false, start + window - now
	}
	return start, count + 1, true, 0
}

// takeRateSlot counts one upload in the client's 5-minute window.
func (s *Server) takeRateSlot(ctx context.Context, user string, limit int64) error {
	tx, err := s.c.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := s.now()
	if _, err := tx.ExecContext(ctx, `INSERT INTO upload_windows (user_id, window_start, uploads) VALUES ($1, $2, 0) ON CONFLICT (user_id) DO NOTHING`, user, now); err != nil {
		return err
	}
	var start, count int64
	if err := tx.QueryRowContext(ctx, `SELECT window_start, uploads FROM upload_windows WHERE user_id = $1 FOR UPDATE`, user).Scan(&start, &count); err != nil {
		return err
	}
	start, count, ok, retry := WindowStep(start, count, now, limit, rateWindowSeconds)
	if !ok {
		return &apiError{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: msgRate, RetryAfter: retry}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE upload_windows SET window_start = $1, uploads = $2 WHERE user_id = $3`, start, count, user); err != nil {
		return err
	}
	return tx.Commit()
}

// checkVolume refuses an upload that would pass the matter's 24-hour caps.
func (s *Server) checkVolume(ctx context.Context, set Settings, matter string, size int64) error {
	var files, total int64
	if err := s.c.DB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(size_bytes), 0) FROM client_uploads WHERE matter_id = $1 AND uploaded_at > $2`,
		matter, s.now()-dayWindowSeconds).Scan(&files, &total); err != nil {
		return err
	}
	if files+1 > set.UploadDailyFiles || total+size > set.UploadDailyMB*megabyte {
		return &apiError{Status: http.StatusRequestEntityTooLarge, Code: "payload_too_large", Message: msgDaily}
	}
	return nil
}

// admitUpload runs every check that needs no bytes: the matter, the size, the daily
// caps and the rate window (which counts this attempt).
func (s *Server) admitUpload(ctx context.Context, v *viewer, matterID string, size int64) (Matter, error) {
	if matterID == "" {
		return Matter{}, &apiError{Status: http.StatusBadRequest, Code: "matter_required", Message: "Choose a matter first."}
	}
	m, err := s.matter(ctx, v, matterID)
	if err != nil {
		return Matter{}, err
	}
	set, err := s.settings(ctx)
	if err != nil {
		return Matter{}, err
	}
	if size <= 0 {
		return Matter{}, badRequest(msgEmpty)
	}
	if size > set.UploadMaxFileMB*megabyte {
		return Matter{}, &apiError{Status: http.StatusRequestEntityTooLarge, Code: "payload_too_large",
			Message: fmt.Sprintf("That file is larger than %d MB.", set.UploadMaxFileMB)}
	}
	if err := s.checkVolume(ctx, set, m.ID, size); err != nil {
		return Matter{}, err
	}
	if err := s.takeRateSlot(ctx, v.UserID, set.UploadRatePer5Min); err != nil {
		return Matter{}, err
	}
	return m, nil
}

// coreUpload is the core's upload session (FileUpload).
type coreUpload struct {
	ID         string `json:"id"`
	ChunkBytes int64  `json:"chunk_size_bytes"`
	NextOffset int64  `json:"next_offset"`
	SizeBytes  int64  `json:"size_bytes"`
	Status     string `json:"status"`
}

func (s *Server) createCoreUpload(ctx context.Context, v *viewer, matter, name string, size int64, ft FileType) (coreUpload, error) {
	var u coreUpload
	err := s.api.Post(ctx, v.Token, "/api/v1/file-uploads", map[string]any{"file_name": name, "matter_id": matter,
		"size_bytes": size, "media_type": ft.MediaType}, &u)
	return u, err
}

func (s *Server) putCoreChunk(ctx context.Context, v *viewer, upload string, start int64, chunk []byte, total int64) (coreUpload, error) {
	var u coreUpload
	err := s.api.Do(ctx, coreapi.Request{Method: http.MethodPut, Path: "/api/v1/file-uploads/" + upload, Viewer: v.Token,
		Body: bytes.NewReader(chunk), Headers: map[string]string{"Content-Type": "application/octet-stream",
			"Content-Range": fmt.Sprintf("bytes %d-%d/%d", start, start+int64(len(chunk))-1, total)}}, &u)
	return u, err
}

// publish completes the core upload, publishes it on the matter shared with the
// client, records it in the 24-hour ledger and tells the matter team.
func (s *Server) publish(ctx context.Context, v *viewer, m Matter, upload, name string, size int64) (File, error) {
	if err := s.api.Post(ctx, v.Token, "/api/v1/file-uploads/"+upload+"/complete", nil, nil); err != nil {
		return File{}, err
	}
	var f File
	if err := s.api.Post(ctx, v.Token, "/api/v1/files", map[string]any{"upload_id": upload, "matter_id": m.ID, "name": name,
		"shared_with_client": true}, &f); err != nil {
		return File{}, err
	}
	if _, err := s.c.DB.ExecContext(ctx, `INSERT INTO client_uploads (file_id, user_id, matter_id, size_bytes, uploaded_at) VALUES ($1, $2, $3, $4, $5)
  ON CONFLICT (file_id) DO NOTHING`, f.ID, v.UserID, m.ID, size, s.now()); err != nil {
		return File{}, err
	}
	who := "A client"
	if me, err := s.me(ctx, v); err == nil && me.Name != "" {
		who = me.Name
	}
	if err := s.api.Post(ctx, "", "/api/v1/notifications", map[string]any{"kind": "client_upload", "title": who + " uploaded a document",
		"link": "/modules/" + ID + "/tabs/messages?matter_id=" + m.ID, "audience": "matter_team", "matter_id": m.ID}, nil); err != nil {
		s.logf("client-portal: upload notice on %s: %v", m.ID, err)
	}
	return f, nil
}

// cleanName keeps the file's own name and extension, minus any directory part.
func cleanName(name string) string {
	name = strings.TrimSpace(path.Base(strings.ReplaceAll(name, `\`, "/")))
	if name == "." || name == "/" {
		return ""
	}
	return name
}

// streamUpload relays one whole file read from body: the checks, then the type check
// on the first bytes, then the chunks, then publication.
func (s *Server) streamUpload(ctx context.Context, v *viewer, matterID, name string, size int64, body io.Reader) (File, error) {
	name = cleanName(name)
	if name == "" {
		return File{}, badRequest("The file needs a name.")
	}
	m, err := s.admitUpload(ctx, v, matterID, size)
	if err != nil {
		return File{}, err
	}
	head := make([]byte, min(size, sniffBytes))
	n, err := io.ReadFull(body, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return File{}, badRequest("The upload was cut off.")
	}
	head = head[:n]
	ft, err := SniffType(name, head)
	if err != nil {
		return File{}, badRequest(err.Error())
	}
	u, err := s.createCoreUpload(ctx, v, m.ID, name, size, ft)
	if err != nil {
		return File{}, uploadErr(err)
	}
	chunk := u.ChunkBytes
	if chunk <= 0 {
		chunk = 8 * megabyte
	}
	src := io.MultiReader(bytes.NewReader(head), body)
	buf := make([]byte, chunk)
	var off int64
	for off < size {
		want := min(chunk, size-off)
		n, err := io.ReadFull(src, buf[:want])
		if n > 0 {
			if _, err := s.putCoreChunk(ctx, v, u.ID, off, buf[:n], size); err != nil {
				return File{}, uploadErr(err)
			}
			off += int64(n)
		}
		if err != nil {
			break
		}
	}
	if off != size {
		return File{}, badRequest("The upload was cut off.")
	}
	var extra [1]byte
	if n, _ := src.Read(extra[:]); n > 0 {
		return File{}, badRequest("The file is larger than the size it was sent with.")
	}
	f, err := s.publish(ctx, v, m, u.ID, name, size)
	if err != nil {
		return File{}, uploadErr(err)
	}
	return f, nil
}

// uploadErr maps a core refusal of an upload step to what the client sees.
func uploadErr(err error) error {
	switch coreapi.StatusOf(err) {
	case http.StatusRequestEntityTooLarge:
		return &apiError{Status: http.StatusRequestEntityTooLarge, Code: "payload_too_large", Message: "That file is too large."}
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return badRequest("The firm's system didn't accept that file.")
	}
	return err
}

// --- chunked uploads for agents ---

// pendingUpload is a chunked upload the module admitted, until it completes. It lives
// in memory: the core holds the bytes, and an abandoned session expires in the core.
type pendingUpload struct {
	user, matter, name string
	size, received     int64
	typed              bool
}

type uploadRegistry struct {
	mu sync.Mutex
	m  map[string]*pendingUpload
}

func newUploadRegistry() *uploadRegistry { return &uploadRegistry{m: map[string]*pendingUpload{}} }

func (r *uploadRegistry) get(id, user string) (*pendingUpload, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.m[id]
	if !ok || p.user != user {
		return nil, false
	}
	return p, true
}

func (r *uploadRegistry) put(id string, p *pendingUpload) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[id] = p
}

func (r *uploadRegistry) drop(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, id)
}

func (s *Server) apiCreateUpload(w http.ResponseWriter, r *http.Request, v *viewer) {
	var in struct {
		MatterID  string `json:"matter_id"`
		FileName  string `json:"file_name"`
		SizeBytes int64  `json:"size_bytes"`
	}
	if e := decodeJSON(r, &in); e != nil {
		writeJSONError(w, e)
		return
	}
	name := cleanName(in.FileName)
	if name == "" {
		writeJSONError(w, badRequest("The file needs a name."))
		return
	}
	m, err := s.admitUpload(r.Context(), v, in.MatterID, in.SizeBytes)
	if err != nil {
		s.fail(w, r, true, err)
		return
	}
	// The media type is decided by the first chunk; the core's session takes the
	// declared size and the name.
	var u coreUpload
	if err := s.api.Post(r.Context(), v.Token, "/api/v1/file-uploads", map[string]any{"file_name": name, "matter_id": m.ID, "size_bytes": in.SizeBytes}, &u); err != nil {
		s.fail(w, r, true, uploadErr(err))
		return
	}
	s.uploads.put(u.ID, &pendingUpload{user: v.UserID, matter: m.ID, name: name, size: in.SizeBytes})
	writeJSON(w, http.StatusCreated, map[string]any{"id": u.ID, "chunk_size_bytes": u.ChunkBytes, "next_offset": 0, "size_bytes": in.SizeBytes})
}

func parseRange(h string) (start, end, total int64, ok bool) {
	rest, found := strings.CutPrefix(h, "bytes ")
	if !found {
		return 0, 0, 0, false
	}
	rng, tot, found := strings.Cut(rest, "/")
	a, b, found2 := strings.Cut(rng, "-")
	if !found || !found2 {
		return 0, 0, 0, false
	}
	var err1, err2, err3 error
	start, err1 = strconv.ParseInt(a, 10, 64)
	end, err2 = strconv.ParseInt(b, 10, 64)
	total, err3 = strconv.ParseInt(tot, 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || end < start {
		return 0, 0, 0, false
	}
	return start, end, total, true
}

func (s *Server) apiPutChunk(w http.ResponseWriter, r *http.Request, v *viewer) {
	id := r.PathValue("upload_id")
	p, ok := s.uploads.get(id, v.UserID)
	if !ok {
		writeJSONError(w, errNotFound)
		return
	}
	start, end, total, ok := parseRange(r.Header.Get("Content-Range"))
	if !ok || total != p.size || start != p.received || end >= p.size {
		writeJSONError(w, badRequest("Content-Range must be bytes start-end/total and continue the upload."))
		return
	}
	chunk, err := io.ReadAll(io.LimitReader(r.Body, end-start+2))
	if err != nil || int64(len(chunk)) != end-start+1 {
		writeJSONError(w, badRequest("The chunk doesn't match its Content-Range."))
		return
	}
	if start == 0 {
		if _, err := SniffType(p.name, chunk[:min(int64(len(chunk)), sniffBytes)]); err != nil {
			s.uploads.drop(id)
			writeJSONError(w, badRequest(err.Error()))
			return
		}
		p.typed = true
	}
	u, err := s.putCoreChunk(r.Context(), v, id, start, chunk, p.size)
	if err != nil {
		s.fail(w, r, true, uploadErr(err))
		return
	}
	p.received = end + 1
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "next_offset": p.received, "size_bytes": p.size, "status": u.Status})
}

func (s *Server) apiCompleteUpload(w http.ResponseWriter, r *http.Request, v *viewer) {
	id := r.PathValue("upload_id")
	p, ok := s.uploads.get(id, v.UserID)
	if !ok {
		writeJSONError(w, errNotFound)
		return
	}
	if !p.typed || p.received != p.size {
		writeJSONError(w, badRequest("The upload isn't finished."))
		return
	}
	m, err := s.matter(r.Context(), v, p.matter)
	if err != nil {
		s.fail(w, r, true, err)
		return
	}
	f, err := s.publish(r.Context(), v, m, id, p.name, p.size)
	if err != nil {
		s.fail(w, r, true, uploadErr(err))
		return
	}
	s.uploads.drop(id)
	writeJSON(w, http.StatusCreated, f)
}

// uploadPost is the page's upload: the script sends each file as its own request
// (raw bytes, with name, size and matter_id in the query), so one bad file in a batch
// fails alone and the others land.
func (s *Server) uploadPost(w http.ResponseWriter, r *http.Request, v *viewer) {
	q := r.URL.Query()
	size, err := strconv.ParseInt(q.Get("size"), 10, 64)
	if err != nil {
		writeJSONError(w, badRequest("The upload has no size."))
		return
	}
	f, err := s.streamUpload(r.Context(), v, q.Get("matter_id"), q.Get("name"), size, r.Body)
	if err != nil {
		s.fail(w, r, true, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": f.ID, "name": f.Name})
}


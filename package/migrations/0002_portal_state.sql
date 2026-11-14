-- Small pieces of portal state. A sandboxed frame has no cookies, so the client's
-- active matter lives here; file_views drives the "new" dot (inline views only);
-- client_uploads is the per-matter 24-hour ledger for the volume caps; upload_windows
-- is the fixed 5-minute upload window per client user.
CREATE TABLE portal_preferences (
  user_id TEXT PRIMARY KEY REFERENCES core.users (id) ON DELETE RESTRICT,
  active_matter_id TEXT REFERENCES core.matters (id) ON DELETE RESTRICT,
  updated_at BIGINT NOT NULL
);

CREATE TABLE file_views (
  file_id TEXT NOT NULL REFERENCES core.files (id) ON DELETE RESTRICT,
  user_id TEXT NOT NULL REFERENCES core.users (id) ON DELETE RESTRICT,
  first_viewed_at BIGINT NOT NULL,
  last_viewed_at BIGINT NOT NULL,
  PRIMARY KEY (file_id, user_id)
);

CREATE TABLE client_uploads (
  file_id TEXT PRIMARY KEY REFERENCES core.files (id) ON DELETE RESTRICT,
  user_id TEXT NOT NULL REFERENCES core.users (id) ON DELETE RESTRICT,
  matter_id TEXT NOT NULL REFERENCES core.matters (id) ON DELETE RESTRICT,
  size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
  uploaded_at BIGINT NOT NULL
);
CREATE INDEX client_uploads_matter_time ON client_uploads (matter_id, uploaded_at);

CREATE TABLE upload_windows (
  user_id TEXT PRIMARY KEY REFERENCES core.users (id) ON DELETE RESTRICT,
  window_start BIGINT NOT NULL,
  uploads INTEGER NOT NULL CHECK (uploads >= 0)
);

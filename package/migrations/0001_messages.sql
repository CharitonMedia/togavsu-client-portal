-- Client portal messages: matter-scoped, one client contact per thread. Staff and
-- clients are both core users, so one author column covers both sides. Messages are
-- never deleted; a retracted one keeps its body and shows as "Message removed".
CREATE TABLE message_threads (
  id TEXT PRIMARY KEY,
  matter_id TEXT NOT NULL REFERENCES core.matters (id) ON DELETE RESTRICT,
  contact_id TEXT NOT NULL REFERENCES core.contacts (id) ON DELETE RESTRICT,
  subject TEXT NOT NULL DEFAULT '',
  created_by_user_id TEXT NOT NULL REFERENCES core.users (id) ON DELETE RESTRICT,
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived', 'closed')),
  last_message_at BIGINT NOT NULL,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL
);
CREATE INDEX message_threads_matter_last ON message_threads (matter_id, last_message_at);
CREATE INDEX message_threads_contact_last ON message_threads (contact_id, last_message_at);

CREATE TABLE messages (
  id TEXT PRIMARY KEY,
  thread_id TEXT NOT NULL REFERENCES message_threads (id) ON DELETE RESTRICT,
  author_user_id TEXT NOT NULL REFERENCES core.users (id) ON DELETE RESTRICT,
  author_kind TEXT NOT NULL CHECK (author_kind IN ('staff', 'client')),
  body TEXT NOT NULL,
  read_at BIGINT,
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'retracted')),
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL
);
CREATE INDEX messages_thread_created ON messages (thread_id, created_at);
CREATE INDEX messages_unread ON messages (thread_id, author_kind, read_at);

CREATE TABLE message_attachments (
  message_id TEXT NOT NULL REFERENCES messages (id) ON DELETE RESTRICT,
  file_id TEXT NOT NULL REFERENCES core.files (id) ON DELETE RESTRICT,
  created_at BIGINT NOT NULL,
  PRIMARY KEY (message_id, file_id)
);

CREATE TABLE message_notifications (
  message_id TEXT PRIMARY KEY REFERENCES messages (id) ON DELETE RESTRICT,
  status TEXT NOT NULL CHECK (status IN ('sent', 'failed', 'skipped')),
  detail TEXT NOT NULL DEFAULT '',
  attempted_at BIGINT NOT NULL
);

CREATE VIEW thread_summary AS
  SELECT t.matter_id,
         SUM(CASE WHEN t.status = 'active' THEN 1 ELSE 0 END) AS active_threads,
         SUM(COALESCE(u.unread, 0)) AS unread_from_client,
         MAX(t.last_message_at) AS last_message_at
    FROM message_threads t
    LEFT JOIN (SELECT thread_id, COUNT(*) AS unread FROM messages
                WHERE author_kind = 'client' AND read_at IS NULL AND status = 'active'
                GROUP BY thread_id) u ON u.thread_id = t.id
   GROUP BY t.matter_id;

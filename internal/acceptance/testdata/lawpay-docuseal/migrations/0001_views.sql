-- The three published views, each over a table the test fills. Invoices live in the
-- core's billing records, not core.invoices, so invoice_id is a plain key.
CREATE TABLE invoice_payability_rows (
  invoice_id TEXT PRIMARY KEY,
  payable BOOLEAN NOT NULL,
  unpayable_reason TEXT NOT NULL DEFAULT '',
  in_progress_cents BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE matter_payability_rows (
  matter_id TEXT PRIMARY KEY REFERENCES core.matters (id) ON DELETE RESTRICT,
  trust_deposits_enabled BOOLEAN NOT NULL,
  minimum_deposit_cents BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE signature_requests_rows (
  signing_request_id TEXT PRIMARY KEY,
  file_id TEXT NOT NULL REFERENCES core.files (id) ON DELETE RESTRICT,
  matter_id TEXT NOT NULL REFERENCES core.matters (id) ON DELETE RESTRICT,
  signer_contact_id TEXT NOT NULL REFERENCES core.contacts (id) ON DELETE RESTRICT,
  status TEXT NOT NULL,
  sent_at BIGINT NOT NULL DEFAULT 0,
  expires_at BIGINT
);

CREATE VIEW invoice_payability AS SELECT * FROM invoice_payability_rows;
CREATE VIEW matter_payability AS SELECT * FROM matter_payability_rows;
CREATE VIEW signature_requests AS SELECT * FROM signature_requests_rows;

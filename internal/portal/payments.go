package portal

import (
	"context"
	"net/http"
	"net/url"

	"github.com/CharitonMedia/togavsu-client-portal/internal/coreapi"
)

// Payments and signing come from the lawpay-docuseal module's published views (SPEC
// section 7), read with the client's context token so the core applies the client's
// access to each keyed record. When that module is missing or disabled the view read
// answers 404; when the admin declined the optional scope, 403. Either way, and on any
// other failure, the portal shows no Pay, Make a deposit or Sign control and every
// page still works. The portal never decides an amount or a destination: it opens the
// pay and sign tabs with explicit ids only.

// Provider is the module that publishes the views and serves the pay and sign tabs.
const Provider = "lawpay-docuseal"

// InvoicePayability is a row of invoice_payability.
type InvoicePayability struct {
	InvoiceID       string `json:"invoice_id"`
	Payable         bool   `json:"payable"`
	UnpayableReason string `json:"unpayable_reason"`
	InProgressCents int64  `json:"in_progress_cents"`
}

// MatterPayability is a row of matter_payability.
type MatterPayability struct {
	MatterID             string `json:"matter_id"`
	TrustDepositsEnabled bool   `json:"trust_deposits_enabled"`
	MinimumDepositCents  int64  `json:"minimum_deposit_cents"`
}

// SignatureRequest is a row of signature_requests.
type SignatureRequest struct {
	FileID           string `json:"file_id"`
	SigningRequestID string `json:"signing_request_id"`
	MatterID         string `json:"matter_id"`
	SignerContactID  string `json:"signer_contact_id"`
	Status           string `json:"status"`
	SentAt           int64  `json:"sent_at"`
	ExpiresAt        *int64 `json:"expires_at"`
}

// readView reads one of the provider's views for the record ids. ok is false when the
// view isn't available to this viewer for any reason.
func readView[T any](ctx context.Context, s *Server, v *viewer, view string, ids []string) ([]T, bool) {
	if len(ids) == 0 {
		return nil, true
	}
	q := url.Values{}
	for _, id := range ids {
		q.Add("record_id", id)
	}
	var out struct {
		Data []T `json:"data"`
	}
	if err := s.api.Get(ctx, v.Token, "/api/v1/modules/"+Provider+"/views/"+view+"?"+q.Encode(), &out); err != nil {
		if st := coreapi.StatusOf(err); st != http.StatusNotFound && st != http.StatusForbidden {
			s.logf("client-portal: %s view %s: %v", Provider, view, err)
		}
		return nil, false
	}
	return out.Data, true
}

// invoicePayability maps each listed invoice to its row; an invoice with no row is not
// payable.
func (s *Server) invoicePayability(ctx context.Context, v *viewer, ids []string) map[string]InvoicePayability {
	rows, _ := readView[InvoicePayability](ctx, s, v, "invoice_payability", ids)
	out := map[string]InvoicePayability{}
	for _, r := range rows {
		out[r.InvoiceID] = r
	}
	return out
}

// depositsEnabled reports whether a client may make a trust deposit on the matter.
func (s *Server) depositsEnabled(ctx context.Context, v *viewer, matter string) bool {
	rows, _ := readView[MatterPayability](ctx, s, v, "matter_payability", []string{matter})
	for _, r := range rows {
		if r.MatterID == matter && r.TrustDepositsEnabled {
			return true
		}
	}
	return false
}

// signatures lists, per file, the signature requests whose signer is the viewer's own
// contact. Another signer's row on the same file shows nothing.
func (s *Server) signatures(ctx context.Context, v *viewer, contact string, fileIDs []string) map[string][]SignatureRequest {
	rows, _ := readView[SignatureRequest](ctx, s, v, "signature_requests", fileIDs)
	out := map[string][]SignatureRequest{}
	for _, r := range rows {
		if r.SignerContactID == contact {
			out[r.FileID] = append(out[r.FileID], r)
		}
	}
	return out
}

// payLink opens the provider's pay tab for one invoice, top level.
func payLink(invoiceID string) string {
	return portalPath(Provider, "pay", "purpose", "invoice_pay", "invoice_id", invoiceID,
		"return_to", portalPath(ID, "invoices", "invoice_id", invoiceID))
}

// depositLink opens the provider's pay tab for a trust deposit on one matter; an
// active replenishment request makes the provider charge that request's amount.
func depositLink(matterID, replenishmentID string) string {
	return portalPath(Provider, "pay", "purpose", "trust_deposit", "matter_id", matterID,
		"replenishment_request_id", replenishmentID, "return_to", portalPath(ID, "home"))
}

// signLink opens the provider's sign tab for one signing request.
func signLink(requestID string) string {
	return portalPath(Provider, "sign", "signing_request_id", requestID, "return_to", portalPath(ID, "documents"))
}

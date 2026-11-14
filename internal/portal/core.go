package portal

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/CharitonMedia/togavsu-client-portal/internal/coreapi"
)

// Core records as the core API returns them. Only the fields the module reads are
// declared; the core's access rules decide which records and fields reach a viewer.

// Me is GET /api/v1/me.
type Me struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Roles              []string `json:"roles"`
	LinkedContactID    *string  `json:"linked_contact_id"`
	AccessEndedMatters int      `json:"access_ended_matters"`
}

// Matter is a core matter (for a client, the CORE-11 field allowlist).
type Matter struct {
	ID                        string   `json:"id"`
	Number                    string   `json:"number"`
	Name                      string   `json:"name"`
	Status                    string   `json:"status"`
	OpenedOn                  string   `json:"opened_on"`
	ClosedOn                  *string  `json:"closed_on"`
	ResponsibleAttorneyUserID *string  `json:"responsible_attorney_user_id"`
	ResponsibleAttorneyName   string   `json:"responsible_attorney_name"`
	AssignedStaffUserIDs      []string `json:"assigned_staff_user_ids"`
	Included                  map[string][]map[string]any `json:"included,omitempty"`
}

// Label is the matter's number and name.
func (m Matter) Label() string {
	return strings.TrimSpace(m.Number + " " + m.Name)
}

// Party is a matter party.
type Party struct {
	ID        string `json:"id"`
	MatterID  string `json:"matter_id"`
	ContactID string `json:"contact_id"`
	Role      string `json:"role"`
}

// Contact is a core contact.
type Contact struct {
	ID            string `json:"id"`
	FullName      string `json:"full_name"`
	PreferredName string `json:"preferred_name"`
}

// Name is the contact's name as shown in the portal.
func (c Contact) Name() string {
	if c.FullName != "" {
		return c.FullName
	}
	return c.PreferredName
}

// File is a core file record.
type File struct {
	ID               string  `json:"id"`
	CreatedAt        string  `json:"created_at"`
	Name             string  `json:"name"`
	Extension        string  `json:"extension"`
	MatterID         *string `json:"matter_id"`
	Origin           string  `json:"origin"`
	Source           string  `json:"source"`
	AttorneyOnly     bool    `json:"attorney_only"`
	SharedWithClient bool    `json:"shared_with_client"`
	CurrentVersionID *string `json:"current_version_id"`
	TrashedAt        *string `json:"trashed_at"`
}

// OnMatter reports whether the file is on matter id.
func (f File) OnMatter(id string) bool { return f.MatterID != nil && *f.MatterID == id }

// Invoice is a core invoice (for a client, the InvoiceClient allowlist).
type Invoice struct {
	ID              string `json:"id"`
	MatterID        string `json:"matter_id"`
	Number          string `json:"number"`
	IssuedOn        string `json:"issued_on"`
	Status          string `json:"status"`
	TotalCents      int64  `json:"total_cents"`
	AmountPaidCents int64  `json:"amount_paid_cents"`
	BalanceDueCents int64  `json:"balance_due_cents"`
	ClientMessage   string `json:"client_message"`
}

// Replenishment is a client's active trust replenishment request (CORE-12 allowlist).
type Replenishment struct {
	ID          string `json:"id"`
	MatterID    string `json:"matter_id"`
	AmountCents int64  `json:"amount_cents"`
	Message     string `json:"message"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
}

// TrustMatter is one row of GET /api/v1/me/trust (CORE-12).
type TrustMatter struct {
	MatterID              string          `json:"matter_id"`
	BalanceCents          int64           `json:"balance_cents"`
	ReplenishmentRequests []Replenishment `json:"replenishment_requests"`
}

// TrustEntry is one row of GET /api/v1/me/trust-ledger (CORE-12).
type TrustEntry struct {
	Date        string `json:"date"`
	Type        string `json:"type"`
	AmountCents int64  `json:"amount_cents"`
	Description string `json:"description"`
}

// Appointment is one row of GET /api/v1/me/appointments (CORE-15).
type Appointment struct {
	Title    string `json:"title"`
	Kind     string `json:"kind"`
	Start    string `json:"start"`
	End      string `json:"end"`
	AllDay   bool   `json:"all_day"`
	Location string `json:"location"`
	MatterID string `json:"matter_id"`
}

// Address is a mailing address.
type Address struct {
	Line1      string `json:"line1"`
	Line2      string `json:"line2"`
	City       string `json:"city"`
	Region     string `json:"region"`
	PostalCode string `json:"postal_code"`
	Country    string `json:"country"`
}

// MyContact is GET /api/v1/me/contact (CORE-13).
type MyContact struct {
	Email          string  `json:"email"`
	Phone          string  `json:"phone"`
	MailingAddress Address `json:"mailing_address"`
}

// FirmProfile is GET /api/v1/firm-profile (CORE-18).
type FirmProfile struct {
	Name         string         `json:"name"`
	ShortName    string         `json:"short_name"`
	Phone        string         `json:"phone"`
	Email        string         `json:"email"`
	Website      string         `json:"website"`
	ClientPortal map[string]any `json:"client_portal"`
}

// Short is the firm's short name, or its name.
func (f FirmProfile) Short() string {
	if f.ShortName != "" {
		return f.ShortName
	}
	return f.Name
}

// PortalLabel is the client portal label from Settings > Appearance.
func (f FirmProfile) PortalLabel() string {
	if v, ok := f.ClientPortal["label"].(string); ok && v != "" {
		return v
	}
	return "Client Portal"
}

// HeaderColor is the client header color from Settings > Appearance, when it is a
// plain #rrggbb color.
func (f FirmProfile) HeaderColor() string {
	v, _ := f.ClientPortal["client_header"].(string)
	if len(v) == 7 && v[0] == '#' && strings.Trim(v[1:], "0123456789abcdefABCDEF") == "" {
		return v
	}
	return "#1f2a44"
}

// DirectoryUser is a row of GET /api/v1/user-directory (CORE-19).
type DirectoryUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ClientAccess is GET /api/v1/client-access (CORE-14).
type ClientAccess struct {
	UserID             string  `json:"user_id"`
	Email              string  `json:"email"`
	Active             bool    `json:"active"`
	MustChangePassword bool    `json:"must_change_password"`
	LastSignInAt       *string `json:"last_sign_in_at"`
	GrantedAt          *string `json:"granted_at"`
}

// NavTab is a row of GET /api/v1/nav/client-context (CORE-10).
type NavTab struct {
	ModuleID string `json:"module_id"`
	TabID    string `json:"tab_id"`
	Title    string `json:"title"`
}

// --- reads ---

func (s *Server) me(ctx context.Context, v *viewer) (Me, error) {
	if v.me != nil {
		return *v.me, nil
	}
	var m Me
	if err := s.api.Get(ctx, v.Token, "/api/v1/me", &m); err != nil {
		return Me{}, err
	}
	v.me = &m
	return m, nil
}

func (s *Server) matter(ctx context.Context, v *viewer, id string) (Matter, error) {
	if m, ok := v.matters[id]; ok {
		return m, nil
	}
	if v.missing[id] {
		return Matter{}, &coreapi.Error{Status: http.StatusNotFound, Code: "not_found"}
	}
	var m Matter
	if !validID(id) {
		return Matter{}, &coreapi.Error{Status: http.StatusNotFound, Code: "not_found"}
	}
	err := s.api.Get(ctx, v.Token, "/api/v1/matters/"+url.PathEscape(id), &m)
	if coreapi.IsNotFound(err) || coreapi.StatusOf(err) == http.StatusForbidden {
		v.missing[id] = true
		return Matter{}, &coreapi.Error{Status: http.StatusNotFound, Code: "not_found"}
	}
	if err != nil {
		return Matter{}, err
	}
	v.matters[id] = m
	return m, nil
}

// canRead reports whether the viewer may read the matter, deciding by the core.
func (s *Server) canRead(ctx context.Context, v *viewer, id string) (bool, error) {
	_, err := s.matter(ctx, v, id)
	if coreapi.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

func (s *Server) listMatters(ctx context.Context, v *viewer, query string) ([]Matter, error) {
	path := "/api/v1/matters?limit=100"
	if query != "" {
		path += "&" + query
	}
	ms, err := coreapi.List[Matter](ctx, s.api, v.Token, path)
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		v.matters[m.ID] = m
	}
	return ms, nil
}

func (s *Server) parties(ctx context.Context, v *viewer, matter string) ([]Party, error) {
	return coreapi.List[Party](ctx, s.api, v.Token, "/api/v1/matter-parties?limit=100&matter_id="+url.QueryEscape(matter))
}

// clientParties are the contacts with the client role on the matter.
func (s *Server) clientParties(ctx context.Context, v *viewer, matter string) ([]Party, error) {
	ps, err := s.parties(ctx, v, matter)
	if err != nil {
		return nil, err
	}
	var out []Party
	seen := map[string]bool{}
	for _, p := range ps {
		if p.Role == "client" && !seen[p.ContactID] {
			seen[p.ContactID] = true
			out = append(out, p)
		}
	}
	return out, nil
}

func (s *Server) contact(ctx context.Context, v *viewer, id string) (Contact, error) {
	if c, ok := v.contacts[id]; ok {
		return c, nil
	}
	var c Contact
	if err := s.api.Get(ctx, v.Token, "/api/v1/contacts/"+url.PathEscape(id), &c); err != nil {
		return Contact{}, err
	}
	v.contacts[id] = c
	return c, nil
}

func (s *Server) file(ctx context.Context, v *viewer, id string) (File, error) {
	if !validID(id) {
		return File{}, &coreapi.Error{Status: http.StatusNotFound, Code: "not_found"}
	}
	var f File
	err := s.api.Get(ctx, v.Token, "/api/v1/files/"+url.PathEscape(id), &f)
	return f, err
}

func (s *Server) files(ctx context.Context, v *viewer, matter string) ([]File, error) {
	return coreapi.List[File](ctx, s.api, v.Token, "/api/v1/files?limit=100&matter_id="+url.QueryEscape(matter))
}

func (s *Server) firm(ctx context.Context, v *viewer) FirmProfile {
	var f FirmProfile
	if err := s.api.Get(ctx, v.Token, "/api/v1/firm-profile", &f); err != nil {
		return FirmProfile{Name: "Your law firm"}
	}
	return f
}

// staffNames labels staff authors (CORE-19). Only ids from threads the viewer passed
// are asked for.
func (s *Server) staffNames(ctx context.Context, v *viewer, ids []string) map[string]string {
	out := map[string]string{}
	if len(ids) == 0 {
		return out
	}
	q := url.Values{}
	seen := map[string]bool{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			q.Add("ids", id)
		}
	}
	rows, err := coreapi.List[DirectoryUser](ctx, s.api, v.Token, "/api/v1/user-directory?"+q.Encode())
	if err != nil {
		return out
	}
	for _, u := range rows {
		out[u.ID] = u.Name
	}
	return out
}

// event writes a timeline row as the viewer. A failed timeline write never undoes the
// action it records; it is reported in the module log.
func (s *Server) event(ctx context.Context, v *viewer, action, recordType, recordID, matter, summary string, clientVisible bool) {
	body := map[string]any{"action": action, "record_type": recordType, "record_id": recordID,
		"matter_id": matter, "summary": summary, "client_visible": clientVisible}
	if err := s.api.Post(ctx, v.Token, "/api/v1/events", body, nil); err != nil {
		s.logf("client-portal: timeline %s on %s: %v", action, matter, err)
	}
}

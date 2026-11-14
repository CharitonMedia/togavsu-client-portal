package portal

import (
	"context"
	"net/http"

	"github.com/CharitonMedia/togavsu-client-portal/internal/coreapi"
)

// The Portal access panel relays to the core's client access endpoints (CORE-14). The
// core creates the client user, sets temporary passwords, mails them by system mail
// and refuses a user who holds any role besides client; the module never sees a
// password except the one the core returns once for staff to read by phone, which it
// shows once and never stores or logs.

// AccessRow is one client party's portal status.
type AccessRow struct {
	ContactID string        `json:"contact_id"`
	Name      string        `json:"name"`
	Access    *ClientAccess `json:"access"`
}

// AccessResult is what an access action returns.
type AccessResult struct {
	Action            string `json:"action"`
	ContactID         string `json:"contact_id"`
	UserID            string `json:"user_id,omitempty"`
	Created           bool   `json:"created,omitempty"`
	Email             string `json:"email,omitempty"`
	MailStatus        string `json:"mail_status,omitempty"`
	TemporaryPassword string `json:"temporary_password,omitempty"`
}

// accessMatter is the matter an access call is about: the matter item's matter claim,
// or the matter_id the agent named.
func (s *Server) accessMatter(ctx context.Context, v *viewer, named string) (Matter, error) {
	id := v.MatterID
	if named != "" {
		id = named
	}
	if id == "" {
		return Matter{}, badRequest("Open portal access from a matter.")
	}
	return s.matter(ctx, v, id)
}

// accessRows lists every client party on the matter with their portal status.
func (s *Server) accessRows(ctx context.Context, v *viewer, m Matter) ([]AccessRow, error) {
	ps, err := s.clientParties(ctx, v, m.ID)
	if err != nil {
		return nil, err
	}
	var out []AccessRow
	for _, p := range ps {
		row := AccessRow{ContactID: p.ContactID}
		if c, err := s.contact(ctx, v, p.ContactID); err == nil {
			row.Name = c.Name()
		}
		var acc ClientAccess
		err := s.api.Get(ctx, v.Token, "/api/v1/client-access?contact_id="+p.ContactID, &acc)
		switch {
		case err == nil:
			a := acc
			row.Access = &a
		case coreapi.IsNotFound(err):
		default:
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// accessAction runs one of the panel's four actions for a client party on the matter.
func (s *Server) accessAction(ctx context.Context, v *viewer, m Matter, contactID, action string) (AccessResult, error) {
	switch action {
	case "grant", "temporary-password", "deactivate", "reactivate":
	default:
		return AccessResult{}, errNotFound
	}
	if !validID(contactID) {
		return AccessResult{}, badRequest("That contact isn't a client on this matter.")
	}
	ps, err := s.clientParties(ctx, v, m.ID)
	if err != nil {
		return AccessResult{}, err
	}
	party := false
	for _, p := range ps {
		party = party || p.ContactID == contactID
	}
	if !party {
		return AccessResult{}, badRequest("That contact isn't a client on this matter.")
	}
	res := AccessResult{Action: action, ContactID: contactID}
	coreErr := func(err error) error {
		if coreapi.StatusOf(err) == http.StatusConflict {
			return conflict("This person also signs in as firm staff, so the portal can't manage their sign-in.")
		}
		if coreapi.StatusOf(err) == http.StatusBadRequest || coreapi.StatusOf(err) == http.StatusUnprocessableEntity {
			return badRequest("This contact has no email address. Add one to the contact first.")
		}
		return err
	}
	if action == "grant" {
		var out struct {
			UserID     string `json:"user_id"`
			Created    bool   `json:"created"`
			Email      string `json:"email"`
			MailStatus string `json:"mail_status"`
		}
		if err := s.api.Post(ctx, v.Token, "/api/v1/client-access", map[string]any{"contact_id": contactID}, &out); err != nil {
			return AccessResult{}, coreErr(err)
		}
		res.UserID, res.Created, res.Email, res.MailStatus = out.UserID, out.Created, out.Email, out.MailStatus
		return res, nil
	}
	var acc ClientAccess
	if err := s.api.Get(ctx, v.Token, "/api/v1/client-access?contact_id="+contactID, &acc); err != nil {
		if coreapi.IsNotFound(err) {
			return AccessResult{}, badRequest("This client has no portal access yet. Use Send portal access first.")
		}
		return AccessResult{}, coreErr(err)
	}
	res.UserID, res.Email = acc.UserID, acc.Email
	path := "/api/v1/client-access/" + acc.UserID + "/" + action
	if action == "temporary-password" {
		var out struct {
			TemporaryPassword string `json:"temporary_password"`
		}
		if err := s.api.Post(ctx, v.Token, path, nil, &out); err != nil {
			return AccessResult{}, coreErr(err)
		}
		res.TemporaryPassword = out.TemporaryPassword
		return res, nil
	}
	if err := s.api.Post(ctx, v.Token, path, nil, nil); err != nil {
		return AccessResult{}, coreErr(err)
	}
	return res, nil
}

func (s *Server) apiListAccess(w http.ResponseWriter, r *http.Request, v *viewer) {
	m, err := s.accessMatter(r.Context(), v, r.URL.Query().Get("matter_id"))
	if err != nil {
		s.fail(w, r, true, err)
		return
	}
	rows, err := s.accessRows(r.Context(), v, m)
	if err != nil {
		s.fail(w, r, true, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"matter_id": m.ID, "data": rows})
}

func (s *Server) apiAccessAction(w http.ResponseWriter, r *http.Request, v *viewer) {
	var body struct {
		MatterID string `json:"matter_id"`
	}
	if r.ContentLength != int64(zero) {
		if e := decodeJSON(r, &body); e != nil {
			writeJSONError(w, e)
			return
		}
	}
	m, err := s.accessMatter(r.Context(), v, body.MatterID)
	if err != nil {
		s.fail(w, r, true, err)
		return
	}
	res, err := s.accessAction(r.Context(), v, m, r.PathValue("contact_id"), r.PathValue("action"))
	if err != nil {
		s.fail(w, r, true, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

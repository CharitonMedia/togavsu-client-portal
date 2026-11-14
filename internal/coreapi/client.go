// Package coreapi is the module's client for the Togavsu core API. Every call carries
// the module's own bearer token. A viewer call also carries the viewer's context token,
// so the core answers exactly what that user may see; a service call (no context token)
// runs as the module's service principal, only where the core allows one (CORE-01).
package coreapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ContextHeader carries a frame's context token on a module's calls back to the core.
const ContextHeader = "X-Togavsu-Context-Token"

// maxErrorBody bounds how much of an error answer is read.
const maxErrorBody = 64 << 10

// Client calls the core.
type Client struct {
	Base  string
	Token string
	HTTP  *http.Client
}

// Error is a core answer other than 2xx.
type Error struct {
	Status int
	Code   string
	Detail string
	Method string
	Path   string
}

func (e *Error) Error() string {
	return fmt.Sprintf("core %s %s: %d %s %s", e.Method, e.Path, e.Status, e.Code, e.Detail)
}

// StatusOf returns the core status of err, or 0 when err isn't a core answer.
func StatusOf(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Status
	}
	return int(0)
}

// CodeOf returns the core problem code of err.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// IsNotFound reports a core 404.
func IsNotFound(err error) bool { return StatusOf(err) == http.StatusNotFound }

// Request is one call.
type Request struct {
	Method  string
	Path    string
	Viewer  string // context token; empty for a service call
	JSON    any
	Body    io.Reader
	Headers map[string]string
}

// Raw sends the request and returns the response for the caller to read and close.
// A non-2xx answer is returned as *Error with the body consumed.
func (c *Client) Raw(ctx context.Context, rq Request) (*http.Response, error) {
	body := rq.Body
	if rq.JSON != nil {
		raw, err := json.Marshal(rq.JSON)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, rq.Method, strings.TrimRight(c.Base, "/")+rq.Path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if rq.Viewer != "" {
		req.Header.Set(ContextHeader, rq.Viewer)
	}
	if rq.JSON != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range rq.Headers {
		req.Header.Set(k, v)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		defer resp.Body.Close()
		e := &Error{Status: resp.StatusCode, Method: rq.Method, Path: rq.Path}
		var p struct {
			Code   string `json:"code"`
			Detail string `json:"detail"`
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		if json.Unmarshal(raw, &p) == nil {
			e.Code, e.Detail = p.Code, p.Detail
		}
		return nil, e
	}
	return resp, nil
}

// Do sends the request and decodes a JSON answer into out (when out is not nil).
func (c *Client) Do(ctx context.Context, rq Request, out any) error {
	resp, err := c.Raw(ctx, rq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Get is a viewer (or service) GET decoded into out.
func (c *Client) Get(ctx context.Context, viewer, path string, out any) error {
	return c.Do(ctx, Request{Method: http.MethodGet, Path: path, Viewer: viewer}, out)
}

// Post is a JSON POST decoded into out.
func (c *Client) Post(ctx context.Context, viewer, path string, body, out any) error {
	if body == nil {
		body = map[string]any{}
	}
	return c.Do(ctx, Request{Method: http.MethodPost, Path: path, Viewer: viewer, JSON: body}, out)
}

// Patch is a JSON PATCH decoded into out.
func (c *Client) Patch(ctx context.Context, viewer, path string, body, out any) error {
	return c.Do(ctx, Request{Method: http.MethodPatch, Path: path, Viewer: viewer, JSON: body}, out)
}

// Page is the core's list envelope cursor.
type Page struct {
	Limit      int     `json:"limit"`
	NextCursor *string `json:"next_cursor"`
}

// List reads every page of a list endpoint. path may carry a query already.
func List[T any](ctx context.Context, c *Client, viewer, path string) ([]T, error) {
	var all []T
	cursor := ""
	for {
		p := path
		if cursor != "" {
			sep := "?"
			if strings.Contains(p, "?") {
				sep = "&"
			}
			p += sep + "cursor=" + url.QueryEscape(cursor)
		}
		var page struct {
			Data []T  `json:"data"`
			Page Page `json:"page"`
		}
		if err := c.Get(ctx, viewer, p, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		if page.Page.NextCursor == nil || *page.Page.NextCursor == "" {
			return all, nil
		}
		cursor = *page.Page.NextCursor
	}
}

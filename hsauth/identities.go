package hsauth

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
	"time"
)

var (
	ErrInvalidIdentityRequest     = errors.New("invalid identity request")
	ErrInvalidAPIKey              = errors.New("invalid api key")
	ErrIdentityManagementDisabled = errors.New("identity management disabled for app")
	ErrIdentityNotFound           = errors.New("identity not found")
	ErrEmailTaken                 = errors.New("email already registered in realm")
)

type Identity struct {
	ID            string `json:"id"`
	RealmID       string `json:"realm_id"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Disabled      bool   `json:"disabled"`
}

type IdentityUpdate struct {
	Email    *string `json:"email,omitempty"`
	Disabled *bool   `json:"disabled,omitempty"`
}

// Identities manages the Identities in the API key's App's Realm.
type Identities interface {
	Create(ctx context.Context, email, password string) (Identity, error)
	List(ctx context.Context) ([]Identity, error)
	Get(ctx context.Context, id string) (Identity, error)
	Update(ctx context.Context, id string, u IdentityUpdate) (Identity, error)
	ResetPassword(ctx context.Context, id, password string) error
	RevokeSessions(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
}

type identitiesClient struct {
	origin string
	apiKey string
	http   *http.Client
}

var _ Identities = (*identitiesClient)(nil)

func NewIdentities(cfg Config) Identities {
	return &identitiesClient{
		origin: strings.TrimRight(cfg.HomeostaticOrigin, "/"),
		apiKey: cfg.APIKey,
		http:   &http.Client{Timeout: time.Duration(cfg.HttpClientTimeout)},
	}
}

func (c *identitiesClient) identityURL(id, resource string) string {
	u := c.origin + "/api/v1/identities"
	if id != "" {
		u += "/" + url.PathEscape(id)
	}
	if resource != "" {
		u += "/" + resource
	}
	return u
}

// do sends the request and decodes a JSON response into out (if non-nil) when the
// status matches want. The response body is never put into errors: it may hold the email.
func (c *identitiesClient) do(ctx context.Context, method, endpoint string, body any, want int, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != want {
		return identityStatusError(method, resp.StatusCode)
	}

	if out == nil {
		return nil
	}

	return json.NewDecoder(resp.Body).Decode(out)
}

func identityStatusError(method string, status int) error {
	switch status {
	case http.StatusBadRequest:
		return ErrInvalidIdentityRequest
	case http.StatusUnauthorized:
		return ErrInvalidAPIKey
	case http.StatusForbidden:
		return ErrIdentityManagementDisabled
	case http.StatusNotFound:
		return ErrIdentityNotFound
	case http.StatusConflict:
		return ErrEmailTaken
	default:
		return fmt.Errorf("identity %s returned %d", strings.ToLower(method), status)
	}
}

func (c *identitiesClient) Create(ctx context.Context, email, password string) (Identity, error) {
	var id Identity
	err := c.do(ctx, http.MethodPost, c.identityURL("", ""),
		map[string]string{"email": email, "password": password}, http.StatusCreated, &id)
	return id, err
}

func (c *identitiesClient) List(ctx context.Context) ([]Identity, error) {
	var ids []Identity
	err := c.do(ctx, http.MethodGet, c.identityURL("", ""), nil, http.StatusOK, &ids)
	return ids, err
}

func (c *identitiesClient) Get(ctx context.Context, id string) (Identity, error) {
	var out Identity
	err := c.do(ctx, http.MethodGet, c.identityURL(id, ""), nil, http.StatusOK, &out)
	return out, err
}

func (c *identitiesClient) Update(ctx context.Context, id string, u IdentityUpdate) (Identity, error) {
	var out Identity
	err := c.do(ctx, http.MethodPatch, c.identityURL(id, ""), u, http.StatusOK, &out)
	return out, err
}

func (c *identitiesClient) ResetPassword(ctx context.Context, id, password string) error {
	return c.do(ctx, http.MethodPost, c.identityURL(id, "password"),
		map[string]string{"password": password}, http.StatusNoContent, nil)
}

func (c *identitiesClient) RevokeSessions(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, c.identityURL(id, "sessions"), nil, http.StatusNoContent, nil)
}

func (c *identitiesClient) Delete(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, c.identityURL(id, ""), nil, http.StatusNoContent, nil)
}

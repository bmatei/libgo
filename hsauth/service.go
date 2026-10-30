package hsauth

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/bmatei/libgo/config"
	"github.com/bmatei/libgo/observability/logs"
)

const (
	stateCookie   = "homeostatic_oauth_state"
	accessCookie  = "app_access"
	refreshCookie = "app_refresh"

	defaultAfterLoginPath = "/dashboard"
	defaultRefreshPath    = "/refresh"

	// jwksRefetchCooldown bounds how often an unknown kid can trigger a JWKS fetch,
	// so forged tokens or webhooks can't make us hammer Homeostatic.
	jwksRefetchCooldown = time.Minute
)

var (
	errInvalidToken = errors.New("invalid access token")
	errUnknownKey   = errors.New("unknown signing key")
)

var (
	ErrWrongPassword = errors.New("current password is incorrect")
	ErrBadPassword   = errors.New("new password rejected")
	ErrRateLimited   = errors.New("rate limited")
)

// RateLimitedError is returned when Homeostatic answers 429; errors.Is(err, ErrRateLimited)
// matches it. RetryAfter is zero when Homeostatic didn't say.
type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("rate limited, retry after %s", e.RetryAfter)
}

func (e *RateLimitedError) Is(target error) bool {
	return target == ErrRateLimited
}

type HomeostaticProfile struct {
	FullName      string `json:"full_name"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
}

type SessionView struct {
	FamilyID  string  `json:"family_id"`
	UserAgent *string `json:"user_agent"`
	IP        *string `json:"ip"`
	CreatedAt string  `json:"created_at"`
	Current   bool    `json:"current"`
}

type Tokens struct {
	AccessToken  string
	RefreshToken string
}

type Config struct {
	HomeostaticOrigin string          `env:"HOMEOSTATIC_ORIGIN" env-default:"http://localhost:8082"`
	HomeostaticIssuer string          `env:"HOMEOSTATIC_ISSUER" env-default:"auth.homeostatic.ro"`
	AppID             string          `env:"APP_ID"`
	APIKey            string          `env:"API_KEY"`
	CallbackURL       string          `env:"CALLBACK_URL" env-default:"http://localhost:8080/callback"`
	AfterLoginPath    string          `env:"AFTER_LOGIN_PATH" env-default:"/dashboard"`
	LoginErrorPath    string          `env:"LOGIN_ERROR_PATH"`
	RefreshPath       string          `env:"REFRESH_PATH" env-default:"/refresh"`
	HttpClientTimeout config.Duration `env:"HTTP_CLIENT_TIMEOUT" env-default:"10s"`
}

type Service interface {
	AuthorizeURL(state string) string
	RegisterURL(state string) string
	AfterLoginPath() string
	LoginErrorPath() string

	Exchange(ctx context.Context, code, userAgent, clientIP string) (Tokens, error)
	Refresh(ctx context.Context, refreshToken, userAgent, clientIP string) (Tokens, error)
	Verify(ctx context.Context, accessToken string) (sub, familyID string, err error)
	PublicKey(ctx context.Context, kid string) (*rsa.PublicKey, error)

	EstablishSession(w http.ResponseWriter, tokens Tokens)
	ClearSession(w http.ResponseWriter)
	Session(r *http.Request) (sub, familyID string, ok bool)
	ListSessions(ctx context.Context, accessToken string) ([]SessionView, error)
	RevokeSession(ctx context.Context, accessToken, familyID string) error
	RevokeOtherSessions(ctx context.Context, accessToken string) error

	GetHomeostaticProfile(ctx context.Context, accessToken string) (HomeostaticProfile, error)
	SetFullName(ctx context.Context, accessToken, fullName string) (HomeostaticProfile, error)
	ChangePassword(ctx context.Context, accessToken, current, newPassword string) error
	DeleteAccount(ctx context.Context, accessToken, current string) error
	ResendVerification(ctx context.Context, accessToken string) error
}

type client struct {
	cfg         Config
	issuer      string
	audience    string
	pubKeyCache map[string]*rsa.PublicKey
	pubKeysURL  string
	lastFetch   time.Time
	mu          sync.Mutex
	http        *http.Client
}

var _ Service = (*client)(nil)

// New returns nil if a configured path is not a same-origin path.
func New(cfg Config) *client {
	cfg.HomeostaticOrigin = strings.TrimRight(cfg.HomeostaticOrigin, "/")

	if cfg.AfterLoginPath == "" {
		cfg.AfterLoginPath = defaultAfterLoginPath
	}

	if cfg.RefreshPath == "" {
		cfg.RefreshPath = defaultRefreshPath
	}

	logger := logs.FromContext(context.Background())
	for name, path := range map[string]string{
		"AFTER_LOGIN_PATH": cfg.AfterLoginPath,
		"REFRESH_PATH":     cfg.RefreshPath,
	} {
		if !isLocalPath(path) {
			logger.Error().Str("path", path).Msgf("Invalid %s: must be a same-origin path", name)
			return nil
		}
	}

	if cfg.LoginErrorPath != "" && !isLocalPath(cfg.LoginErrorPath) {
		logger.Error().Str("path", cfg.LoginErrorPath).Msg("Invalid LOGIN_ERROR_PATH: must be a same-origin path")
		return nil
	}

	return &client{
		cfg:        cfg,
		http:       &http.Client{Timeout: time.Duration(cfg.HttpClientTimeout)},
		issuer:     cfg.HomeostaticIssuer,
		audience:   cfg.AppID,
		pubKeysURL: cfg.HomeostaticOrigin + "/.well-known/jwks.json",
	}
}

// isLocalPath rejects anything a browser could resolve to another origin.
func isLocalPath(p string) bool {
	return strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") && !strings.HasPrefix(p, "/\\")
}

func (c *client) AfterLoginPath() string {
	return c.cfg.AfterLoginPath
}

func (c *client) LoginErrorPath() string {
	return c.cfg.LoginErrorPath
}

func (c *client) AuthorizeURL(state string) string {
	return c.flowURL("/authorize", state)
}

func (c *client) RegisterURL(state string) string {
	return c.flowURL("/register", state)
}

func (c *client) flowURL(path, state string) string {
	return c.cfg.HomeostaticOrigin + path + "?" + url.Values{
		"app":          {c.cfg.AppID},
		"redirect_uri": {c.cfg.CallbackURL},
		"state":        {state},
	}.Encode()
}

func (c *client) Exchange(ctx context.Context, code, userAgent, clientIP string) (Tokens, error) {
	return c.tokenRequest(ctx, "/api/v1/token", url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {c.cfg.CallbackURL},
	}, userAgent, clientIP)
}

func (c *client) Refresh(ctx context.Context, refreshToken, userAgent, clientIP string) (Tokens, error) {
	return c.tokenRequest(ctx, "/api/v1/refresh", url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}, userAgent, clientIP)
}

func (c *client) tokenRequest(ctx context.Context, path string, form url.Values, userAgent, clientIP string) (Tokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.HomeostaticOrigin+path, strings.NewReader(form.Encode()))
	if err != nil {
		return Tokens{}, err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(c.cfg.AppID, c.cfg.APIKey)

	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}

	if clientIP != "" {
		req.Header.Set("X-Forwarded-For", clientIP)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Tokens{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return Tokens{}, fmt.Errorf("token endpoint returned %d", resp.StatusCode)
	}

	var body struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Tokens{}, err
	}

	if body.AccessToken == "" {
		return Tokens{}, errors.New("token endpoint returned no access_token")
	}

	return Tokens{AccessToken: body.AccessToken, RefreshToken: body.RefreshToken}, nil
}

func (c *client) Verify(ctx context.Context, accessToken string) (string, string, error) {
	var claims jwt.MapClaims
	_, err := jwt.ParseWithClaims(accessToken, &claims, func(t *jwt.Token) (interface{}, error) {
		kid, _ := t.Header["kid"].(string)
		return c.PublicKey(ctx, kid)
	},
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(c.issuer),
		jwt.WithAudience(c.audience),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return "", "", errInvalidToken
	}

	sub, err := claims.GetSubject()
	if err != nil || sub == "" {
		return "", "", errInvalidToken
	}

	familyID, _ := claims["family_id"].(string)
	return sub, familyID, nil
}

func (c *client) identitySessionsURL(familyID string) string {
	u := c.cfg.HomeostaticOrigin + "/api/v1/sessions"
	if familyID != "" {
		u += "/" + url.PathEscape(familyID)
	}
	return u
}

func (c *client) identityRequest(ctx context.Context, method, endpoint, accessToken string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-API-Key", c.cfg.APIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.http.Do(req)
}

func (c *client) ListSessions(ctx context.Context, accessToken string) ([]SessionView, error) {
	endpoint := c.identitySessionsURL("") + "?" + url.Values{"app_id": {c.cfg.AppID}}.Encode()

	resp, err := c.identityRequest(ctx, http.MethodGet, endpoint, accessToken, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sessions list returned %d", resp.StatusCode)
	}

	var views []SessionView
	if err := json.NewDecoder(resp.Body).Decode(&views); err != nil {
		return nil, err
	}

	return views, nil
}

func (c *client) RevokeSession(ctx context.Context, accessToken, familyID string) error {
	return c.revoke(ctx, c.identitySessionsURL(familyID), accessToken)
}

func (c *client) RevokeOtherSessions(ctx context.Context, accessToken string) error {
	return c.revoke(ctx, c.identitySessionsURL(""), accessToken)
}

func (c *client) revoke(ctx context.Context, endpoint, accessToken string) error {
	resp, err := c.identityRequest(ctx, http.MethodDelete, endpoint, accessToken, nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("sessions revoke returned %d", resp.StatusCode)
	}

	return nil
}

func (c *client) identityMeURL(resource string) string {
	return c.cfg.HomeostaticOrigin + "/api/v1/me/" + resource
}

func (c *client) GetHomeostaticProfile(ctx context.Context, accessToken string) (HomeostaticProfile, error) {
	resp, err := c.identityRequest(ctx, http.MethodGet, c.identityMeURL("profile"), accessToken, nil)
	if err != nil {
		return HomeostaticProfile{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return HomeostaticProfile{}, fmt.Errorf("profile get returned %d", resp.StatusCode)
	}

	var p HomeostaticProfile
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return HomeostaticProfile{}, err
	}

	return p, nil
}

func (c *client) SetFullName(ctx context.Context, accessToken, fullName string) (HomeostaticProfile, error) {
	body, err := json.Marshal(map[string]string{"full_name": fullName})
	if err != nil {
		return HomeostaticProfile{}, err
	}

	resp, err := c.identityRequest(ctx, http.MethodPut, c.identityMeURL("profile"), accessToken, bytes.NewReader(body))
	if err != nil {
		return HomeostaticProfile{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return HomeostaticProfile{}, fmt.Errorf("profile set returned %d", resp.StatusCode)
	}

	var p HomeostaticProfile
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return HomeostaticProfile{}, err
	}

	return p, nil
}

func (c *client) ChangePassword(ctx context.Context, accessToken, current, newPassword string) error {
	body, err := json.Marshal(map[string]string{
		"current_password": current,
		"new_password":     newPassword,
	})
	if err != nil {
		return err
	}

	resp, err := c.identityRequest(ctx, http.MethodPut, c.identityMeURL("password"), accessToken, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil
	case http.StatusForbidden:
		return ErrWrongPassword
	case http.StatusBadRequest:
		return ErrBadPassword
	default:
		return fmt.Errorf("password change returned %d", resp.StatusCode)
	}
}

func (c *client) DeleteAccount(ctx context.Context, accessToken, current string) error {
	body, err := json.Marshal(map[string]string{"current_password": current})
	if err != nil {
		return err
	}

	resp, err := c.identityRequest(ctx, http.MethodDelete, c.identityMeURL("account"), accessToken, bytes.NewReader(body))
	if err != nil {
		return err
	}

	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil
	case http.StatusForbidden:
		return ErrWrongPassword
	default:
		return fmt.Errorf("account delete returned %d", resp.StatusCode)
	}
}

func (c *client) ResendVerification(ctx context.Context, accessToken string) error {
	resp, err := c.identityRequest(ctx, http.MethodPost, c.identityMeURL("verification"), accessToken, nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil
	case http.StatusTooManyRequests:
		return &RateLimitedError{RetryAfter: retryAfter(resp)}
	default:
		return fmt.Errorf("verification resend returned %d", resp.StatusCode)
	}
}

// retryAfter reads the body's retry_after (seconds), falling back to the
// Retry-After header (seconds or HTTP date).
func retryAfter(resp *http.Response) time.Duration {
	var body struct {
		RetryAfter int `json:"retry_after"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body); err == nil && body.RetryAfter > 0 {
		return time.Duration(body.RetryAfter) * time.Second
	}

	h := resp.Header.Get("Retry-After")
	if secs, err := strconv.Atoi(h); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}

	return 0
}

func (c *client) EstablishSession(w http.ResponseWriter, tokens Tokens) {
	http.SetCookie(w, &http.Cookie{
		Name:     accessCookie,
		Value:    tokens.AccessToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookie,
		Value:    tokens.RefreshToken,
		Path:     c.cfg.RefreshPath,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

func (c *client) ClearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: accessCookie, Value: "", Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
	http.SetCookie(w, &http.Cookie{
		Name: refreshCookie, Value: "", Path: c.cfg.RefreshPath,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: -1,
	})
}

func (c *client) Session(r *http.Request) (string, string, bool) {
	cookie, err := r.Cookie(accessCookie)
	if err != nil || cookie.Value == "" {
		return "", "", false
	}

	sub, familyID, err := c.Verify(r.Context(), cookie.Value)
	if err != nil {
		return "", "", false
	}

	return sub, familyID, true
}

// PublicKey returns Homeostatic's JWKS key for kid. An unknown kid refetches the JWKS at
// most once per jwksRefetchCooldown; errUnknownKey means it is still unknown, any
// other error means the JWKS couldn't be fetched.
func (c *client) PublicKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if key, ok := c.pubKeyCache[kid]; ok {
		return key, nil
	}

	if time.Since(c.lastFetch) < jwksRefetchCooldown {
		return nil, errUnknownKey
	}
	c.lastFetch = time.Now()

	keys, err := c.fetchPubKeys(ctx)
	if err != nil {
		return nil, err
	}
	c.pubKeyCache = keys

	key, ok := keys[kid]
	if !ok {
		return nil, errUnknownKey
	}

	return key, nil
}

func (c *client) fetchPubKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.pubKeysURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks endpoint returned %d", resp.StatusCode)
	}

	var doc struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, err
	}

	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" {
			continue
		}

		pub, err := rsaPublicKeyFromJWK(k.N, k.E)
		if err != nil {
			return nil, err
		}

		keys[k.Kid] = pub
	}

	return keys, nil
}

func rsaPublicKeyFromJWK(n, e string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(n)
	if err != nil {
		return nil, err
	}

	eBytes, err := base64.RawURLEncoding.DecodeString(e)
	if err != nil {
		return nil, err
	}

	exp := 0
	for _, b := range eBytes {
		exp = exp<<8 | int(b)
	}

	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: exp}, nil
}

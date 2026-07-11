package awareauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"github.com/bmatei/libgo/observability/logs"
	"github.com/julienschmidt/httprouter"
)

const dashboardPath = "/dashboard"

type LoginHook interface {
	OnLogin(ctx context.Context, sub string) error
}

type subKeyType string

const SubKey subKeyType = "sub"

func Sub(ctx context.Context) string {
	v, _ := ctx.Value(SubKey).(string)
	return v
}

type familyKeyType string

const FamilyKey familyKeyType = "family"

func Family(ctx context.Context) string {
	v, _ := ctx.Value(FamilyKey).(string)
	return v
}

func Login(svc Service) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		state, err := randomToken()
		if err != nil {
			http.Error(w, `{"error":"failed to generate state (crypto error)"}`, http.StatusInternalServerError)
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     stateCookie,
			Value:    state,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   600,
		})
		http.Redirect(w, r, svc.AuthorizeURL(state), http.StatusFound)
	}
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func Callback(svc Service, hook LoginHook) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		ctx := r.Context()
		logger := logs.FromContext(ctx)

		cookie, err := r.Cookie(stateCookie)
		if err != nil || cookie.Value == "" || cookie.Value != r.URL.Query().Get("state") {
			http.Error(w, "invalid state", http.StatusBadRequest)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: stateCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})

		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}

		tokens, err := svc.Exchange(ctx, code, r.Header.Get("User-Agent"), clientIP(r))
		if err != nil {
			logger.Error().Err(err).Msg("Code exchange failed")
			http.Error(w, "code exchange failed", http.StatusBadGateway)
			return
		}

		sub, _, err := svc.Verify(ctx, tokens.AccessToken)
		if err != nil {
			logger.Error().Err(err).Msg("Access token verification failed")
			http.Error(w, "invalid access token", http.StatusBadGateway)
			return
		}

		if hook != nil {
			if err := hook.OnLogin(ctx, sub); err != nil {
				logger.Error().Err(err).Msg("Login hook failed; continuing (fail-open)")
			}
		}

		svc.EstablishSession(w, tokens)
		http.Redirect(w, r, dashboardPath, http.StatusFound)
	}
}

func Me(svc Service) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		sub, _, ok := svc.Session(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"sub": sub})
	}
}

func Refresh(svc Service) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		ctx := r.Context()
		logger := logs.FromContext(ctx)

		cookie, err := r.Cookie(refreshCookie)
		if err != nil || cookie.Value == "" {
			svc.ClearSession(w)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no refresh token"})
			return
		}

		tokens, err := svc.Refresh(ctx, cookie.Value, r.Header.Get("User-Agent"), clientIP(r))
		if err != nil {
			logger.Info().Err(err).Msg("Refresh rejected; clearing session")
			svc.ClearSession(w)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session expired"})
			return
		}

		svc.EstablishSession(w, tokens)
		w.WriteHeader(http.StatusOK)
	}
}

func accessToken(r *http.Request) string {
	cookie, err := r.Cookie(accessCookie)
	if err != nil {
		return ""
	}

	return cookie.Value
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
		if first != "" {
			return first
		}
	}

	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}

	return r.RemoteAddr
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

package hsauth

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/bmatei/libgo/observability/logs"
	"github.com/julienschmidt/httprouter"
)

type EraseHook interface {
	OnErase(ctx context.Context, sub string) error
}

func Account(svc Service) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		ctx := r.Context()
		logger := logs.FromContext(ctx)

		p, err := svc.GetHomeostaticProfile(ctx, accessToken(r))
		if err != nil {
			logger.Error().Err(err).Msg("Failed to load Homeostatic profile")
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "couldn't reach Homeostatic, try again"})
			return
		}

		writeJSON(w, http.StatusOK, p)
	}
}

func SetAccount(svc Service) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		ctx := r.Context()
		logger := logs.FromContext(ctx)

		var body struct {
			FullName string `json:"full_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		p, err := svc.SetFullName(ctx, accessToken(r), body.FullName)
		if err != nil {
			logger.Error().Err(err).Msg("Failed to update Homeostatic profile")
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "couldn't reach Homeostatic, try again"})
			return
		}

		writeJSON(w, http.StatusOK, p)
	}
}

func ChangePassword(svc Service) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		ctx := r.Context()
		logger := logs.FromContext(ctx)
		sub := Sub(ctx)

		var body struct {
			CurrentPassword string `json:"current_password"`
			NewPassword     string `json:"new_password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		logger.Info().Str("sub", sub).Msg("Password change requested")

		err := svc.ChangePassword(ctx, accessToken(r), body.CurrentPassword, body.NewPassword)
		switch {
		case err == nil:
			svc.ClearSession(w)
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, ErrWrongPassword):
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "current password is incorrect"})
		case errors.Is(err, ErrBadPassword):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "that new password was rejected"})
		default:
			logger.Error().Err(err).Msg("Password change failed")
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "couldn't reach Homeostatic, try again"})
		}
	}
}

func DeleteAccount(svc Service, hook EraseHook) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		ctx := r.Context()
		logger := logs.FromContext(ctx)
		sub := Sub(ctx)

		var body struct {
			CurrentPassword string `json:"current_password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		logger.Info().Str("sub", sub).Msg("Account deletion requested")

		err := svc.DeleteAccount(ctx, accessToken(r), body.CurrentPassword)
		switch {
		case err == nil:
			if hook != nil {
				if err := hook.OnErase(ctx, sub); err != nil {
					logger.Error().Err(err).Msg("Account erase hook failed; continuing (fail-open)")
				}
			}

			svc.ClearSession(w)
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, ErrWrongPassword):
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "current password is incorrect"})
		default:
			logger.Error().Err(err).Msg("Account deletion failed")
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "couldn't reach Homeostatic, try again"})
		}
	}
}

func ResendVerificationHandler(svc Service) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		ctx := r.Context()
		logger := logs.FromContext(ctx)

		err := svc.ResendVerification(ctx, accessToken(r))
		var rl *RateLimitedError
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.As(err, &rl):
			secs := int(math.Ceil(rl.RetryAfter.Seconds()))
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			writeJSON(w, http.StatusTooManyRequests, map[string]any{
				"error":       "verification email sent recently, try again later",
				"retry_after": secs,
			})
		default:
			logger.Error().Err(err).Msg("Verification resend failed")
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "couldn't reach Homeostatic, try again"})
		}
	}
}

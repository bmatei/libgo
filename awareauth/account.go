package awareauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

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

		p, err := svc.GetAwareProfile(ctx, accessToken(r))
		if err != nil {
			logger.Error().Err(err).Msg("Failed to load Aware profile")
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "couldn't reach Aware, try again"})
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
			logger.Error().Err(err).Msg("Failed to update Aware profile")
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "couldn't reach Aware, try again"})
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
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "couldn't reach Aware, try again"})
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
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "couldn't reach Aware, try again"})
		}
	}
}

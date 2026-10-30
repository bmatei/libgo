package hsauth

import (
	"context"
	"net/http"

	"github.com/bmatei/libgo/observability/logs"
	"github.com/julienschmidt/httprouter"
)

type sessionsResponse struct {
	Sessions []SessionView `json:"sessions"`
}

func Sessions(svc Service) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		ctx := r.Context()
		logger := logs.FromContext(ctx)

		views, err := svc.ListSessions(ctx, accessToken(r))
		if err != nil {
			logger.Error().Err(err).Msg("Failed to list sessions")
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to load sessions"})
			return
		}

		writeJSON(w, http.StatusOK, sessionsResponse{Sessions: views})
	}
}

func RevokeSession(svc Service) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
		ctx := r.Context()
		familyID := ps.ByName("familyId")
		logger := logs.FromContext(ctx)

		if err := svc.RevokeSession(ctx, accessToken(r), familyID); err != nil {
			logger.Error().Err(err).Msg("Failed to revoke session")
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to revoke session"})
			return
		}

		if familyID == Family(ctx) {
			svc.ClearSession(w)
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func RevokeOtherSessions(svc Service) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		ctx := r.Context()
		logger := logs.FromContext(ctx)

		if err := svc.RevokeOtherSessions(ctx, accessToken(r)); err != nil {
			logger.Error().Err(err).Msg("Failed to revoke other sessions")
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to revoke sessions"})
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func RequireSession(svc Service, next httprouter.Handle) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
		sub, familyID, ok := svc.Session(r)
		if !ok {
			http.Error(w, `{"error":"not authenticated"}`, http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), SubKey, sub)
		ctx = context.WithValue(ctx, FamilyKey, familyID)
		next(w, r.WithContext(ctx), params)
	}
}

package hsauth

import (
	"net/http"

	"github.com/bmatei/libgo/observability/logs"
	"github.com/julienschmidt/httprouter"
)

func Logout(svc Service) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		ctx := r.Context()
		logger := logs.FromContext(ctx)

		if _, familyID, ok := svc.Session(r); ok && familyID != "" {
			if err := svc.RevokeSession(ctx, accessToken(r), familyID); err != nil {
				logger.Warn().Err(err).Msg("Logout: family revoke failed; clearing cookies anyway")
			}
		}

		svc.ClearSession(w)

		if r.Method == http.MethodGet {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

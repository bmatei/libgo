package auth

import (
	"net/http"

	"github.com/julienschmidt/httprouter"
)

func AuthenticateBasic(nextHandler httprouter.Handle, config BasicAuthConfig) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
		u, p, ok := r.BasicAuth()
		if !ok || u != config.Username || p != config.Password {
			w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		nextHandler(w, r, params)
	}
}

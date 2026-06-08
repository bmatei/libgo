package server

import (
	"net/http"

	"github.com/julienschmidt/httprouter"
)

func Health() httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"healthy"}`))
	}
}

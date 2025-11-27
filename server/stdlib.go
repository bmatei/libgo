package server

import (
	"net/http"

	"github.com/julienschmidt/httprouter"
)

func FromStdlib(handler http.Handler) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		handler.ServeHTTP(w, r)
	}
}

func ToStdlib(handler httprouter.Handle) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(w, r, nil)
	})
}

package logs

import (
	// "bytes"
	// "io"
	"net"
	"net/http"

	"github.com/julienschmidt/httprouter"
	"github.com/rs/zerolog/log"
)

type ExtraField func(r *http.Request, params httprouter.Params) (string, string)

func LogRequest(nextHandler httprouter.Handle, extras ...ExtraField) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
		ipv4 := r.Header.Get("X-Forwarded-For")
		if ipv4 == "" {
			ipv4, _, _ = net.SplitHostPort(r.RemoteAddr)
		}

		requestId := r.Header.Get("X-Request-Id")
		if requestId == "" {
			requestId = RequestIdFromContext(r.Context())
		}

		pathParams := make(map[string]string)
		for _, p := range params {
			pathParams[p.Key] = p.Value
		}

		queryParams := make(map[string][]string)
		for key, values := range r.URL.Query() {
			queryParams[key] = values
		}

		logger := log.With().
			Str("endpoint", r.URL.String()).
			Str("method", r.Method).
			Str("ip", ipv4).
			Interface("queryParams", queryParams).
			Interface("pathParams", pathParams).
			Str("requestId", requestId).
			Logger()

		for _, extra := range extras {
			key, val := extra(r, params)
			logger = logger.With().Str(key, val).Logger()
		}

		ctx := WithRequestId(
			WithLogger(r.Context(), logger),
			requestId,
		)
		r = r.WithContext(ctx)

		logger.Info().Msg("Got request")

		nextHandler(w, r, params)
	}
}

package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	RequestCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)

	RequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Histogram of response time for HTTP requests",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	InflightRequests = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "http_inflight_requests",
			Help: "Current number of inflight HTTP requests",
		},
		[]string{"method", "path"},
	)
)

func init() {
	prometheus.MustRegister(RequestCounter, RequestDuration, InflightRequests)
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func PrometheusMiddleware(path string, nextHandler httprouter.Handle) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
		start := time.Now()

		InflightRequests.WithLabelValues(r.Method, path).Inc()
		defer InflightRequests.WithLabelValues(r.Method, path).Dec()

		ww := &statusWriter{ResponseWriter: w, status: 200}

		nextHandler(ww, r, params)

		duration := time.Since(start).Seconds()

		RequestDuration.WithLabelValues(r.Method, path).Observe(duration)
		RequestCounter.WithLabelValues(
			r.Method,
			path,
			strconv.Itoa(ww.status),
		).Inc()
	}
}

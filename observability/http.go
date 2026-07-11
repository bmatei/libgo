package observability

import (
	"github.com/julienschmidt/httprouter"

	"github.com/bmatei/libgo/observability/logs"
	"github.com/bmatei/libgo/observability/metrics"
)

func ObservableEndpoint(metricsPath string, h httprouter.Handle) httprouter.Handle {
	return logs.LogRequest(metrics.PrometheusMiddleware(metricsPath, h))
}

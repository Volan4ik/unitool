package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	RequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "bot_requests_total", Help: "Telegram updates"},
		[]string{"type"},
	)
	ReqLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Name: "bot_req_latency_ms", Buckets: prometheus.ExponentialBuckets(5, 2, 10)},
		[]string{"handler"},
	)
)

func Serve(addr string) {
	reg := prometheus.DefaultRegisterer
	reg.MustRegister(RequestsTotal, ReqLatency)
	http.Handle("/metrics", promhttp.Handler())
	go http.ListenAndServe(addr, nil)
}
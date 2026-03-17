package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	CometRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "comet_requests_total", Help: "Comet API calls"},
		[]string{"endpoint", "result", "code"},
	)
	CometLatencyMs = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Name: "comet_latency_ms", Help: "Comet call latency", Buckets: prometheus.ExponentialBuckets(10, 2, 12)},
		[]string{"endpoint", "result"},
	)
	CometRetries = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "comet_retries_total", Help: "Retries performed for Comet calls"},
		[]string{"endpoint"},
	)
	CometStreamLength = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Name: "comet_stream_length_chars", Help: "Length of streamed outputs", Buckets: prometheus.ExponentialBuckets(32, 2, 12)},
		[]string{"endpoint"},
	)
	StreamEdits = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "tg_stream_edits_total", Help: "Telegram stream message edits"},
		[]string{"mode"},
	)
)

func Serve(addr string) {
	reg := prometheus.DefaultRegisterer
	reg.MustRegister(CometRequests, CometLatencyMs, CometRetries, CometStreamLength, StreamEdits)
	http.Handle("/metrics", promhttp.Handler())
	go http.ListenAndServe(addr, nil)
}

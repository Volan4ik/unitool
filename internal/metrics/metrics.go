package metrics

import (
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	registerOnce sync.Once

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

func Register() {
	registerOnce.Do(func() {
		reg := prometheus.DefaultRegisterer
		reg.MustRegister(CometRequests, CometLatencyMs, CometRetries, CometStreamLength, StreamEdits)
	})
}

func Handler() http.Handler {
	Register()
	return promhttp.Handler()
}

func Serve(addr string) {
	Register()
	http.Handle("/metrics", Handler())
	go http.ListenAndServe(addr, nil)
}

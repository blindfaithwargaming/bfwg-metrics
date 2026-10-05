package api

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/blindfaithwargaming/bfwg-metrics/internal/store"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// instrument records RED metrics (rate, errors, duration) and an access log.
// Routes are labelled by mux pattern, not raw path, to bound label cardinality.
func instrument(mux *http.ServeMux, reg *prometheus.Registry, logger *slog.Logger) http.Handler {
	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "bfwg_http_requests_total",
		Help: "HTTP requests by route, method and status code.",
	}, []string{"route", "method", "code"})
	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "bfwg_http_request_duration_seconds",
		Help:    "HTTP request latency by route.",
		Buckets: prometheus.DefBuckets,
	}, []string{"route"})
	reg.MustRegister(requests, duration)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		_, route := mux.Handler(r)
		if route == "" {
			route = "unmatched"
		}
		mux.ServeHTTP(rec, r)
		elapsed := time.Since(start)
		requests.WithLabelValues(route, r.Method, strconv.Itoa(rec.status)).Inc()
		duration.WithLabelValues(route).Observe(elapsed.Seconds())
		logger.Info("http request", "method", r.Method, "route", route,
			"status", rec.status, "elapsed_ms", elapsed.Milliseconds())
	})
}

// etlCollector reads the audit table at scrape time, so the server reports
// ETL health without sharing memory with the separate ETL process.
type etlCollector struct {
	store      *store.Store
	logger     *slog.Logger
	lastRun    *prometheus.Desc
	lastOK     *prometheus.Desc
	rowsLoaded *prometheus.Desc
}

func newETLCollector(s *store.Store, logger *slog.Logger) *etlCollector {
	labels := []string{"source"}
	return &etlCollector{
		store:  s,
		logger: logger,
		lastRun: prometheus.NewDesc("bfwg_etl_last_run_timestamp_seconds",
			"Unix time the most recent ETL run for a source finished.", labels, nil),
		lastOK: prometheus.NewDesc("bfwg_etl_last_run_success",
			"1 if the most recent ETL run for a source succeeded, else 0.", labels, nil),
		rowsLoaded: prometheus.NewDesc("bfwg_etl_last_run_rows_loaded",
			"Rows loaded by the most recent ETL run for a source.", labels, nil),
	}
}

func (c *etlCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.lastRun
	ch <- c.lastOK
	ch <- c.rowsLoaded
}

func (c *etlCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	runs, err := c.store.LatestRuns(ctx)
	if err != nil {
		c.logger.Warn("etl collector query failed", "err", err)
		return
	}
	for _, r := range runs {
		ok := 0.0
		if r.Status == "ok" {
			ok = 1
		}
		ch <- prometheus.MustNewConstMetric(c.lastRun, prometheus.GaugeValue,
			float64(r.FinishedAt.Unix()), r.Source)
		ch <- prometheus.MustNewConstMetric(c.lastOK, prometheus.GaugeValue, ok, r.Source)
		ch <- prometheus.MustNewConstMetric(c.rowsLoaded, prometheus.GaugeValue,
			float64(r.RowsLoaded), r.Source)
	}
}

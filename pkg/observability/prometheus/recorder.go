package prometheus

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/observability"
)

var defaultHistogramBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}

type histogramSample struct {
	sum   float64
	count uint64
	le    map[float64]uint64
}

type Recorder struct {
	mu          sync.Mutex
	counters    map[string]float64
	gauges      map[string]float64
	histograms  map[string]*histogramSample
	bucketEdges []float64
}

func NewRecorder() *Recorder {
	return &Recorder{
		counters:    make(map[string]float64),
		gauges:      make(map[string]float64),
		histograms:  make(map[string]*histogramSample),
		bucketEdges: append([]float64(nil), defaultHistogramBuckets...),
	}
}

func (r *Recorder) IncCounter(_ context.Context, name observability.MetricName, attrs ...observability.Attribute) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters[metricKey(name, attrs)]++
}

func (r *Recorder) ObserveHistogram(_ context.Context, name observability.MetricName, value float64, attrs ...observability.Attribute) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := metricKey(name, attrs)
	sample, ok := r.histograms[key]
	if !ok {
		sample = &histogramSample{le: make(map[float64]uint64, len(r.bucketEdges))}
		r.histograms[key] = sample
	}
	sample.sum += value
	sample.count++
	for _, bound := range r.bucketEdges {
		if value <= bound {
			sample.le[bound]++
		}
	}
}

func (r *Recorder) SetGauge(_ context.Context, name observability.MetricName, value float64, attrs ...observability.Attribute) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gauges[metricKey(name, attrs)] = value
}

// RecordQueueMetrics updates queue depth gauges from a queue that implements JobAdmin.
func (r *Recorder) RecordQueueMetrics(ctx context.Context, queue async.Queue) error {
	metrics, err := async.CollectQueueMetrics(ctx, queue)
	if err != nil {
		return err
	}
	r.SetGauge(ctx, observability.MetricQueueJobsQueued, float64(metrics.Queued))
	r.SetGauge(ctx, observability.MetricQueueJobsRunning, float64(metrics.Running))
	r.SetGauge(ctx, observability.MetricQueueJobsDeadLetter, float64(metrics.DeadLetter))
	return nil
}

func (r *Recorder) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		r.mu.Lock()
		var b strings.Builder
		for _, key := range sortedKeys(r.counters) {
			writeMetricLine(&b, sanitizeMetricName(key), "counter", r.counters[key])
		}
		for _, key := range sortedKeys(r.gauges) {
			writeMetricLine(&b, sanitizeMetricName(key), "gauge", r.gauges[key])
		}
		for _, key := range sortedKeysMap(r.histograms) {
			sample := r.histograms[key]
			base := sanitizeMetricName(key)
			b.WriteString("# TYPE ")
			b.WriteString(base)
			b.WriteString(" histogram\n")
			for _, bound := range r.bucketEdges {
				b.WriteString(base)
				b.WriteString("_bucket{le=\"")
				b.WriteString(fmt.Sprintf("%g", bound))
				b.WriteString("\"} ")
				b.WriteString(fmt.Sprintf("%d", sample.le[bound]))
				b.WriteByte('\n')
			}
			b.WriteString(base)
			b.WriteString("_bucket{le=\"+Inf\"} ")
			b.WriteString(fmt.Sprintf("%d", sample.count))
			b.WriteByte('\n')
			writeMetricLine(&b, base+"_sum", "counter", sample.sum)
			writeMetricLine(&b, base+"_count", "counter", float64(sample.count))
		}
		r.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte(b.String()))
	})
}

func writeMetricLine(b *strings.Builder, name, typ string, value float64) {
	b.WriteString("# TYPE ")
	b.WriteString(name)
	b.WriteString(" ")
	b.WriteString(typ)
	b.WriteByte('\n')
	b.WriteString(name)
	b.WriteString(" ")
	b.WriteString(fmt.Sprintf("%g", value))
	b.WriteByte('\n')
}

func sortedKeys(values map[string]float64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysMap[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func metricKey(name observability.MetricName, attrs []observability.Attribute) string {
	if len(attrs) == 0 {
		return string(name)
	}
	parts := make([]string, 0, len(attrs))
	for _, attr := range attrs {
		parts = append(parts, attr.Key+"="+attr.Value)
	}
	sort.Strings(parts)
	return string(name) + "{" + strings.Join(parts, ",") + "}"
}

func sanitizeMetricName(name string) string {
	replacer := strings.NewReplacer("{", "_", "}", "", "=", "_", ",", "_", "+", "plus", ".", "_", "\"", "")
	return replacer.Replace(name)
}

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
			name, labels := parseMetricKey(key)
			writeSampleLine(&b, name, labels, r.counters[key])
		}
		for _, key := range sortedKeys(r.gauges) {
			name, labels := parseMetricKey(key)
			writeSampleLine(&b, name, labels, r.gauges[key])
		}
		for _, key := range sortedKeysMap(r.histograms) {
			name, labels := parseMetricKey(key)
			sample := r.histograms[key]
			b.WriteString("# TYPE ")
			b.WriteString(name)
			b.WriteString(" histogram\n")
			for _, bound := range r.bucketEdges {
				b.WriteString(name)
				b.WriteString("_bucket")
				b.WriteString(formatHistogramLabels(labels, fmt.Sprintf("%g", bound)))
				b.WriteString(" ")
				b.WriteString(fmt.Sprintf("%d", sample.le[bound]))
				b.WriteByte('\n')
			}
			b.WriteString(name)
			b.WriteString("_bucket")
			b.WriteString(formatHistogramLabels(labels, "+Inf"))
			b.WriteString(" ")
			b.WriteString(fmt.Sprintf("%d", sample.count))
			b.WriteByte('\n')
			writeSampleLine(&b, name+"_sum", labels, sample.sum)
			writeSampleLine(&b, name+"_count", labels, float64(sample.count))
		}
		r.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(b.String()))
	})
}

func writeSampleLine(b *strings.Builder, name string, labels map[string]string, value float64) {
	b.WriteString(name)
	if len(labels) > 0 {
		b.WriteString(formatLabelSuffix(labels))
	}
	b.WriteString(" ")
	b.WriteString(fmt.Sprintf("%g", value))
	b.WriteByte('\n')
}

func formatLabelSuffix(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+quoteLabelValue(labels[key]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func formatHistogramLabels(labels map[string]string, leValue string) string {
	if len(labels) == 0 {
		return fmt.Sprintf(`{le="%s"}`, leValue)
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys)+1)
	for _, key := range keys {
		parts = append(parts, key+"="+quoteLabelValue(labels[key]))
	}
	parts = append(parts, fmt.Sprintf(`le="%s"`, leValue))
	return "{" + strings.Join(parts, ",") + "}"
}

func quoteLabelValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	return `"` + value + `"`
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

func parseMetricKey(key string) (string, map[string]string) {
	idx := strings.Index(key, "{")
	if idx < 0 {
		return key, nil
	}
	if !strings.HasSuffix(key, "}") {
		return key, nil
	}
	name := key[:idx]
	labelPart := key[idx+1 : len(key)-1]
	if labelPart == "" {
		return name, nil
	}
	labels := make(map[string]string)
	for _, part := range strings.Split(labelPart, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		labels[kv[0]] = kv[1]
	}
	return name, labels
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

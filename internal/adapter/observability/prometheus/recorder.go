package prometheus

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/aijustin/agentflow-go/pkg/observability"
)

type Recorder struct {
	mu       sync.Mutex
	counters map[string]float64
}

func NewRecorder() *Recorder {
	return &Recorder{counters: make(map[string]float64)}
}

func (r *Recorder) IncCounter(_ context.Context, name observability.MetricName, attrs ...observability.Attribute) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters[metricKey(name, attrs)]++
}

func (r *Recorder) ObserveHistogram(_ context.Context, name observability.MetricName, value float64, attrs ...observability.Attribute) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters[metricKey(name, attrs)+":sum"] += value
	r.counters[metricKey(name, attrs)+":count"]++
}

func (r *Recorder) SetGauge(_ context.Context, name observability.MetricName, value float64, attrs ...observability.Attribute) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters[metricKey(name, attrs)] = value
}

func (r *Recorder) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		r.mu.Lock()
		keys := make([]string, 0, len(r.counters))
		for key := range r.counters {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var b strings.Builder
		for _, key := range keys {
			b.WriteString("# TYPE ")
			b.WriteString(sanitizeMetricName(key))
			b.WriteString(" counter\n")
			b.WriteString(sanitizeMetricName(key))
			b.WriteString(" ")
			b.WriteString(fmt.Sprintf("%g", r.counters[key]))
			b.WriteByte('\n')
		}
		r.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte(b.String()))
	})
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
	replacer := strings.NewReplacer("{", "_", "}", "", "=", "_", ",", "_")
	return replacer.Replace(name)
}

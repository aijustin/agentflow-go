package prometheus

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	queueinmem "github.com/aijustin/agentflow-go/internal/adapter/queue/inmem"
	"github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/observability"
)

func TestRecorderHandler(t *testing.T) {
	recorder := NewRecorder()
	recorder.IncCounter(context.Background(), observability.MetricRuntimeEventsTotal, observability.Attribute{Key: "event_type", Value: "run"})
	recorder.ObserveHistogram(context.Background(), observability.MetricRunDurationSeconds, 0.42)
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	recorder.Handler().ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.Contains(body, `agentflow_runtime_events_total{event_type="run"} 1`) {
		t.Fatalf("expected labeled counter metric, got %q", body)
	}
	if !strings.Contains(body, `agentflow_run_duration_seconds_bucket{le="+Inf"}`) {
		t.Fatalf("expected histogram buckets, got %q", body)
	}
}

func TestRecorderRecordQueueMetrics(t *testing.T) {
	recorder := NewRecorder()
	queue := queueinmem.NewQueue()
	ctx := context.Background()
	if _, err := queue.Enqueue(ctx, async.Job{ID: "job-1", Type: async.RunJobType, MaxAttempts: 1}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordQueueMetrics(ctx, queue); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	recorder.Handler().ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "agentflow_queue_jobs_queued") {
		t.Fatalf("expected queue gauge, got %q", body)
	}
}

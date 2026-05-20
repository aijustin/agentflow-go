package prometheus

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/observability"
)

func TestRecorderHandler(t *testing.T) {
	recorder := NewRecorder()
	recorder.IncCounter(context.Background(), observability.MetricRuntimeEventsTotal, observability.Attribute{Key: "type", Value: "run"})
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	recorder.Handler().ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "agentflow_runtime_events_total") {
		t.Fatalf("expected metric exposition, got %q", body)
	}
}

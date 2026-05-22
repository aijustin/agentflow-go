package http

import (
	stdhttp "net/http"
	"net/http/httptest"
	"testing"
)

func TestRunsMuxRoutesCheckpointPaths(t *testing.T) {
	var got string
	checkpoint := stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		got = "checkpoint:" + r.URL.Path
	})
	async := stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		got = "async:" + r.URL.Path
	})
	mux := &RunsMux{Checkpoint: checkpoint, Async: async}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(stdhttp.MethodGet, "/v1/runs/run-1/steps", nil))
	if got != "checkpoint:/v1/runs/run-1/steps" {
		t.Fatalf("steps route got=%q", got)
	}

	got = ""
	mux.ServeHTTP(rec, httptest.NewRequest(stdhttp.MethodPost, "/v1/runs/run-1/resume-from-step", nil))
	if got != "checkpoint:/v1/runs/run-1/resume-from-step" {
		t.Fatalf("resume route got=%q", got)
	}

	got = ""
	mux.ServeHTTP(rec, httptest.NewRequest(stdhttp.MethodGet, "/v1/runs/run-1/checkpoints", nil))
	if got != "checkpoint:/v1/runs/run-1/checkpoints" {
		t.Fatalf("checkpoints route got=%q", got)
	}

	got = ""
	mux.ServeHTTP(rec, httptest.NewRequest(stdhttp.MethodGet, "/v1/runs/run-1/checkpoints/3", nil))
	if got != "checkpoint:/v1/runs/run-1/checkpoints/3" {
		t.Fatalf("checkpoint version route got=%q", got)
	}

	got = ""
	mux.ServeHTTP(rec, httptest.NewRequest(stdhttp.MethodPost, "/v1/runs/run-1/resume-from-checkpoint", nil))
	if got != "checkpoint:/v1/runs/run-1/resume-from-checkpoint" {
		t.Fatalf("restore route got=%q", got)
	}

	got = ""
	mux.ServeHTTP(rec, httptest.NewRequest(stdhttp.MethodGet, "/v1/runs/run-1", nil))
	if got != "async:/v1/runs/run-1" {
		t.Fatalf("async route got=%q", got)
	}
}

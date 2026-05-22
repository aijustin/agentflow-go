package http

import (
	"net/http"
	"strings"
)

// RunsMux routes checkpoint Studio paths before falling back to the async runs handler.
type RunsMux struct {
	Checkpoint http.Handler
	Async      http.Handler
}

func (mux *RunsMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if mux.Checkpoint != nil {
		path := r.URL.Path
		if isCheckpointRunPath(path) {
			mux.Checkpoint.ServeHTTP(w, r)
			return
		}
	}
	if mux.Async != nil {
		mux.Async.ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}

func isCheckpointRunPath(path string) bool {
	if len(path) < len("/v1/runs/") {
		return false
	}
	if strings.HasSuffix(path, "/steps") {
		return true
	}
	if strings.HasSuffix(path, "/resume-from-step") {
		return true
	}
	if strings.HasSuffix(path, "/checkpoints") {
		return true
	}
	if strings.HasSuffix(path, "/resume-from-checkpoint") {
		return true
	}
	if strings.HasSuffix(path, "/fork") {
		return true
	}
	return strings.Contains(path, "/checkpoints/")
}

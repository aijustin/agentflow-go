package debugui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	auditinmem "github.com/aijustin/agentflow-go/internal/adapter/audit/inmem"
	"github.com/aijustin/agentflow-go/internal/adapter/auth/apikey"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/security"
)

func TestServerServesIndexAndScenarios(t *testing.T) {
	server, err := New([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}

	index := httptest.NewRecorder()
	server.Handler().ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/", nil))
	if index.Code != http.StatusOK || !bytes.Contains(index.Body.Bytes(), []byte("Agent Debug Console")) {
		t.Fatalf("unexpected index response: %d %s", index.Code, index.Body.String())
	}

	scenarios := httptest.NewRecorder()
	server.Handler().ServeHTTP(scenarios, httptest.NewRequest(http.MethodGet, "/api/scenarios", nil))
	if scenarios.Code != http.StatusOK {
		t.Fatalf("unexpected scenarios response: %d", scenarios.Code)
	}
	var decoded []ScenarioTemplate
	if err := json.Unmarshal(scenarios.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) < 4 {
		t.Fatalf("expected built-in scenarios, got %d", len(decoded))
	}
}

func TestServerRunAutonomousScenario(t *testing.T) {
	server, err := New([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.NewBufferString(`{"scenario_id":"autonomous","prompt":"hello"}`)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/run", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected run response: %d %s", rec.Code, rec.Body.String())
	}
	var record RunRecord
	if err := json.Unmarshal(rec.Body.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record.Status != "completed" || record.Output != "hello" || len(record.Events) == 0 {
		t.Fatalf("unexpected run record: %+v", record)
	}
}

func TestServerRunHITLAndResume(t *testing.T) {
	server, err := New([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.NewBufferString(`{"scenario_id":"hitl","prompt":"approval"}`)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/run", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected run response: %d %s", rec.Code, rec.Body.String())
	}
	var paused RunRecord
	if err := json.Unmarshal(rec.Body.Bytes(), &paused); err != nil {
		t.Fatal(err)
	}
	if paused.Status != "paused" || paused.Token == "" {
		t.Fatalf("unexpected paused record: %+v", paused)
	}

	resumeBody, _ := json.Marshal(map[string]string{"token": paused.Token, "decision": "approve"})
	resume := httptest.NewRecorder()
	server.Handler().ServeHTTP(resume, httptest.NewRequest(http.MethodPost, "/api/resume", bytes.NewReader(resumeBody)))
	if resume.Code != http.StatusOK {
		t.Fatalf("unexpected resume response: %d %s", resume.Code, resume.Body.String())
	}
	var resumed RunRecord
	if err := json.Unmarshal(resume.Body.Bytes(), &resumed); err != nil {
		t.Fatal(err)
	}
	if resumed.Status != "running" {
		t.Fatalf("unexpected resumed record: %+v", resumed)
	}
}

func TestServerProtectsRunAndResumeAPIsWhenConfigured(t *testing.T) {
	authenticator, err := apikey.NewStaticAuthenticator(map[string]identity.Principal{
		"secret-key": {ID: "svc-1", Type: identity.PrincipalService, Scope: identity.Scope{TenantID: "tenant-1"}, Roles: []identity.Role{identity.RoleAdmin}},
		"viewer-key": {ID: "viewer-1", Type: identity.PrincipalUser, Scope: identity.Scope{TenantID: "tenant-1"}, Roles: []identity.Role{identity.RoleViewer}},
	})
	if err != nil {
		t.Fatal(err)
	}
	authn, err := apikey.NewMiddleware(apikey.MiddlewareConfig{Authenticator: authenticator})
	if err != nil {
		t.Fatal(err)
	}
	audits := auditinmem.NewSink(20)
	server, err := New([]byte("secret"), WithHTTPMiddleware(authn), WithSecurityPolicy(security.NewDefaultRolePolicy()), WithAuditSink(audits))
	if err != nil {
		t.Fatal(err)
	}

	unauthorized := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/api/run", bytes.NewBufferString(`{"scenario_id":"hitl","prompt":"approval"}`)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized run request, got %d", unauthorized.Code)
	}
	forbiddenReq := httptest.NewRequest(http.MethodPost, "/api/run", bytes.NewBufferString(`{"scenario_id":"hitl","prompt":"approval"}`))
	forbiddenReq.Header.Set(apikey.DefaultHeaderName, "viewer-key")
	forbidden := httptest.NewRecorder()
	server.Handler().ServeHTTP(forbidden, forbiddenReq)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden run request, got %d", forbidden.Code)
	}

	runReq := httptest.NewRequest(http.MethodPost, "/api/run", bytes.NewBufferString(`{"scenario_id":"hitl","prompt":"approval"}`))
	runReq.Header.Set(apikey.DefaultHeaderName, "secret-key")
	runRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(runRec, runReq)
	if runRec.Code != http.StatusOK {
		t.Fatalf("unexpected authorized run response: %d %s", runRec.Code, runRec.Body.String())
	}
	var paused RunRecord
	if err := json.Unmarshal(runRec.Body.Bytes(), &paused); err != nil {
		t.Fatal(err)
	}
	if paused.Status != "paused" || paused.Token == "" {
		t.Fatalf("unexpected paused record: %+v", paused)
	}

	resumeBody, _ := json.Marshal(map[string]string{"token": paused.Token, "decision": "approve"})
	resumeReq := httptest.NewRequest(http.MethodPost, "/api/resume", bytes.NewReader(resumeBody))
	resumeReq.Header.Set(apikey.DefaultHeaderName, "secret-key")
	resumeRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(resumeRec, resumeReq)
	if resumeRec.Code != http.StatusOK {
		t.Fatalf("unexpected authorized resume response: %d %s", resumeRec.Code, resumeRec.Body.String())
	}

	events := audits.Events()
	if !hasAuditEvent(events, audit.EventPolicyDenied) || !hasAuditEvent(events, audit.EventRunSubmitted) || !hasAuditEvent(events, audit.EventHITLDecided) {
		t.Fatalf("expected denied, submitted, and decided audit events, got %+v", events)
	}
}

func hasAuditEvent(events []audit.Event, typ audit.EventType) bool {
	for _, event := range events {
		if event.Type == typ {
			return true
		}
	}
	return false
}

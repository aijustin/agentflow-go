package agentflow

import (
	"fmt"
	"net/http"

	studiohttp "github.com/aijustin/agentflow-go/internal/adapter/studio/http"
	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/security"
)

type StudioHTTPHandlerConfig struct {
	Framework      *Framework
	StudioSavePath string
	MaxBodyBytes   int64
	// Policy authorizes the mutating endpoints: studio run as run.submit and
	// studio save as admin.configure. When Policy is nil those endpoints
	// default-deny with 403 auth_required — studio run executes agents and
	// studio save rewrites the scenario file, so mounting them open was a
	// privilege-escalation hole. The pure-transform endpoints
	// (validate/codegen/yaml/import-yaml) stay open.
	Policy security.Policy
	// Audit receives policy-denied records when configured.
	Audit audit.Sink
	// InsecureAllowNoAuth disables the default-deny protection on the
	// mutating endpoints when Policy is nil. Only set it behind an
	// authenticating reverse proxy or in tests.
	InsecureAllowNoAuth bool
}

// NewStudioHTTPHandler serves production Studio routes:
//   - POST /v1/studio/validate
//   - POST /v1/studio/codegen
//   - POST /v1/studio/yaml
//   - POST /v1/studio/import-yaml
//   - POST /v1/studio/run
//   - POST /v1/studio/save (when StudioSavePath is set)
//
// The run/save routes require StudioHTTPHandlerConfig.Policy (or an explicit
// InsecureAllowNoAuth opt-out).
func NewStudioHTTPHandler(config StudioHTTPHandlerConfig) (http.Handler, error) {
	if config.Framework == nil {
		return nil, fmt.Errorf("agentflow: studio handler requires framework")
	}
	adapter := &studioFramework{framework: config.Framework, savePath: config.StudioSavePath}
	httpConfig := studiohttp.HandlerConfig{
		Validate:            adapter,
		Codegen:             adapter,
		YAML:                adapter,
		ImportYAML:          adapter,
		Run:                 adapter,
		MaxBodyBytes:        config.MaxBodyBytes,
		Policy:              config.Policy,
		Audit:               config.Audit,
		InsecureAllowNoAuth: config.InsecureAllowNoAuth,
	}
	if config.StudioSavePath != "" {
		httpConfig.Save = adapter
	}
	return studiohttp.NewHandler(httpConfig), nil
}

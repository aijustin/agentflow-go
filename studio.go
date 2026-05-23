package agentflow

import (
	"fmt"
	"net/http"

	studiohttp "github.com/aijustin/agentflow-go/internal/adapter/studio/http"
)

type StudioHTTPHandlerConfig struct {
	Framework      *Framework
	StudioSavePath string
	MaxBodyBytes   int64
}

// NewStudioHTTPHandler serves production Studio routes:
//   - POST /v1/studio/validate
//   - POST /v1/studio/codegen
//   - POST /v1/studio/yaml
//   - POST /v1/studio/run
//   - POST /v1/studio/save (when StudioSavePath is set)
func NewStudioHTTPHandler(config StudioHTTPHandlerConfig) (http.Handler, error) {
	if config.Framework == nil {
		return nil, fmt.Errorf("agentflow: studio handler requires framework")
	}
	adapter := &studioFramework{framework: config.Framework, savePath: config.StudioSavePath}
	httpConfig := studiohttp.HandlerConfig{
		Validate:     adapter,
		Codegen:      adapter,
		YAML:         adapter,
		Run:          adapter,
		MaxBodyBytes: config.MaxBodyBytes,
	}
	if config.StudioSavePath != "" {
		httpConfig.Save = adapter
	}
	return studiohttp.NewHandler(httpConfig), nil
}

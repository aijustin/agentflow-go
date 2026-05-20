package agentflow

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	eventrouterhttp "github.com/aijustin/agentflow-go/internal/adapter/eventrouter/http"
	humanhttp "github.com/aijustin/agentflow-go/internal/adapter/human/http"
	"github.com/aijustin/agentflow-go/pkg/core"
)

type WebhookHTTPHandlerConfig struct {
	Framework    *Framework
	MaxBodyBytes int64
}

type HumanHTTPHandlerConfig struct {
	Framework    *Framework
	MaxBodyBytes int64
}

type webhookFramework struct {
	framework *Framework
}

func (adapter *webhookFramework) HandleEvent(r *http.Request, event IncomingEvent) (any, error) {
	return adapter.framework.HandleEvent(r.Context(), event)
}

type humanFramework struct {
	framework *Framework
}

func (adapter *humanFramework) Resume(ctx context.Context, token string, decision core.Decision, amendment json.RawMessage) error {
	return adapter.framework.Resume(ctx, token, decision, amendment)
}

func (adapter *humanFramework) ResumeAndContinue(ctx context.Context, token string, decision core.Decision, amendment json.RawMessage) (any, error) {
	return adapter.framework.ResumeAndContinue(ctx, token, decision, amendment)
}

// NewWebhookHTTPHandler serves POST / requests that accept IncomingEvent JSON payloads.
func NewWebhookHTTPHandler(config WebhookHTTPHandlerConfig) (http.Handler, error) {
	if config.Framework == nil {
		return nil, fmt.Errorf("agentflow: webhook handler requires framework")
	}
	return eventrouterhttp.NewHandler(eventrouterhttp.HandlerConfig{
		Framework:    &webhookFramework{framework: config.Framework},
		MaxBodyBytes: config.MaxBodyBytes,
	})
}

// NewHumanHTTPHandler serves human gate resume requests. When the request sets
// continue=true, the handler calls ResumeAndContinue instead of Resume.
func NewHumanHTTPHandler(config HumanHTTPHandlerConfig) http.Handler {
	adapter := &humanFramework{framework: config.Framework}
	return humanhttp.NewHandler(humanhttp.HandlerConfig{
		Gate:         adapter,
		Continuer:    adapter,
		MaxBodyBytes: config.MaxBodyBytes,
	})
}

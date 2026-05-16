package http

import (
	"errors"
	"fmt"
	nethttp "net/http"
	"time"

	"github.com/aijustin/agentflow-go/pkg/audit"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/security"
)

type ResourceFunc func(*nethttp.Request) security.Resource

type MiddlewareConfig struct {
	Policy       security.Policy
	Action       security.Action
	Resource     security.Resource
	ResourceFunc ResourceFunc
	Audit        audit.Sink
}

func NewMiddleware(config MiddlewareConfig) (func(nethttp.Handler) nethttp.Handler, error) {
	if config.Policy == nil {
		return nil, fmt.Errorf("authorization middleware: policy is nil")
	}
	if config.Action == "" {
		return nil, fmt.Errorf("authorization middleware: action is required")
	}
	resourceFor := config.ResourceFunc
	if resourceFor == nil {
		resource := config.Resource
		resourceFor = func(*nethttp.Request) security.Resource { return resource }
	}
	return func(next nethttp.Handler) nethttp.Handler {
		return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			resource := resourceFor(r)
			principal, err := identity.RequirePrincipal(r.Context())
			if err != nil {
				recordDenied(r, config.Audit, identity.Principal{}, config.Action, resource, security.ErrUnauthenticated)
				nethttp.Error(w, "unauthorized", nethttp.StatusUnauthorized)
				return
			}
			if err := config.Policy.Authorize(r.Context(), principal, config.Action, resource); err != nil {
				recordDenied(r, config.Audit, principal, config.Action, resource, err)
				status := nethttp.StatusInternalServerError
				message := "authorization failed"
				switch {
				case errors.Is(err, security.ErrUnauthenticated):
					status = nethttp.StatusUnauthorized
					message = "unauthorized"
				case errors.Is(err, security.ErrUnauthorized):
					status = nethttp.StatusForbidden
					message = "forbidden"
				}
				nethttp.Error(w, message, status)
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

func recordDenied(r *nethttp.Request, sink audit.Sink, principal identity.Principal, action security.Action, resource security.Resource, reason error) {
	if sink == nil {
		return
	}
	_ = sink.Record(r.Context(), audit.Event{
		Type:      audit.EventPolicyDenied,
		Timestamp: time.Now().UTC(),
		Principal: principal,
		Action:    action,
		Resource:  resource,
		RunID:     resource.ID,
		Outcome:   "denied",
		Reason:    reason.Error(),
	})
}

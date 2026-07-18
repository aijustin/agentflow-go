package runtime

import (
	"context"

	"github.com/aijustin/agentflow-go/pkg/toolorch"
)

type samplingStepKey struct{}

func contextWithSamplingStep(ctx context.Context, step toolorch.SamplingStepContext) context.Context {
	return context.WithValue(ctx, samplingStepKey{}, step)
}

func samplingStepFromContext(ctx context.Context) (toolorch.SamplingStepContext, bool) {
	step, ok := ctx.Value(samplingStepKey{}).(toolorch.SamplingStepContext)
	return step, ok
}

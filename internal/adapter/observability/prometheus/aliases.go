package prometheus

import pkgprom "github.com/aijustin/agentflow-go/pkg/observability/prometheus"

type Recorder = pkgprom.Recorder

var NewRecorder = pkgprom.NewRecorder

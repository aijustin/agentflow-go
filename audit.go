package agentflow

import (
	auditfile "github.com/aijustin/agentflow-go/internal/adapter/audit/file"
	auditinmem "github.com/aijustin/agentflow-go/internal/adapter/audit/inmem"
	"github.com/aijustin/agentflow-go/pkg/audit"
)

func NewNoopAuditSink() audit.Sink {
	return audit.NoopSink()
}

func NewInMemoryAuditSink(limit int) audit.Sink {
	return auditinmem.NewSink(limit)
}

func NewFileAuditSink(path string) (audit.Sink, error) {
	return auditfile.NewSink(path)
}

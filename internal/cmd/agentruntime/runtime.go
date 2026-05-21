package agentruntime

import (
	"database/sql"
	"io"
	"net/http"

	agentflow "github.com/aijustin/agentflow-go"
	asyncpkg "github.com/aijustin/agentflow-go/pkg/async"
)

type Config = agentflow.ProductionConfig

var LoadConfigFromEnv = agentflow.LoadProductionConfigFromEnv

var IsLoopbackAddr = agentflow.IsLoopbackAddr

func NewFramework(config Config, tokenWriter io.Writer) (*agentflow.Framework, error) {
	return agentflow.NewProduction(config, tokenWriter)
}

func NewQueue(config Config, db **sql.DB) (asyncpkg.Queue, error) {
	return agentflow.NewProductionQueue(config, db)
}

func NewProductionHandler(config Config, fw *agentflow.Framework, queue asyncpkg.Queue) (http.Handler, error) {
	return agentflow.BuildProductionHTTPHandler(config, fw, queue)
}

func NewWorker(config Config, queue asyncpkg.Queue, fw *agentflow.Framework) (*asyncpkg.Worker, error) {
	return agentflow.NewProductionWorker(config, queue, fw)
}

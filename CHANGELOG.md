# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.3.0] - 2026-07-23

### Changed

- **BREAKING — convenience constructors moved out of the root package.** The root package now contains only the Framework core (`Framework`, `RunRequest`, options, lifecycle, retention, lease, wiring validation). Nothing was renamed and no signatures changed; only the import paths below moved. There are no deprecated forwarding shims or type aliases left behind.
- **BREAKING — `api.go` (`ProductionHTTPHandlerConfig`, `NewProductionHTTPHandler`) moved to `pkg/httpx` as well**: it composes the other HTTP constructors, and root cannot import `pkg/httpx` (which itself imports root). `ValidateWiring`/`WiringOptions`/`WithRequireLLM` stay in root: their signature and bodies are inseparable from the unexported root options machinery.
- Migration table (old `agentflow.X` → new):

  | Symbols | New import |
  |---|---|
  | HTTP constructors: `CheckpointHTTPHandlerConfig`, `NewCheckpointHTTPHandler`, `RetentionHTTPHandlerConfig`, `NewRetentionHTTPHandler`, `StudioHTTPHandlerConfig`, `NewStudioHTTPHandler`, `WebhookHTTPHandlerConfig`, `HumanHTTPHandlerConfig`, `NewWebhookHTTPHandler`, `NewHumanHTTPHandler`, `AsyncRunHTTPHandlerConfig`, `NewAsyncRunHTTPHandler`, `ProductionHTTPHandlerConfig`, `NewProductionHTTPHandler`, `ObservabilityHTTPHandlerConfig`, `NewObservabilityHTTPHandler`, `KnowledgeRegistry`, `KnowledgeWiringOptions`, `MCPRegistry`, `WireMCPTools`, `MCPWiringOptions` | `github.com/aijustin/agentflow-go/pkg/httpx` |
  | Run-state / blob / memory / queue: `NewInMemoryRunStateRepository`, `NewInMemoryCheckpointHistory`, `NewPostgresCheckpointHistory`, `NewInMemoryBlobStore`, `NewFileRunStateRepository`, `NewPostgresRunStateRepository`, `RedisRunStateRepositoryConfig`, `NewRedisRunStateRepository`, `NewFileBlobStore`, `NewFileMemoryRepository`, `S3BlobStoreConfig`, `NewS3BlobStore`, `NewInMemoryJobQueue`, `NewPostgresJobQueue` | `github.com/aijustin/agentflow-go/pkg/adapters` |
  | LLM providers: `OpenAICompatibleProvider`, `LLMProviderRouter`, `NewOpenAICompatibleGateway`, `NewOpenAICompatibleProvider`, `NewOpenAICompatibleEmbedder`, `NewLocalGateway`, `NewAnthropicGateway`, `NewLLMRouter`, `NewLLMProviderRouter` | `github.com/aijustin/agentflow-go/pkg/adapters` |
  | Catalog manifests: `LoadToolManifestFile`, `LoadToolManifest`, `ValidateToolManifest`, `LoadSkillManifestFile`, `LoadSkillManifest`, `ValidateSkillManifest` | `github.com/aijustin/agentflow-go/pkg/adapters` |
  | Knowledge: `RetrieverToolConfig`, `PostgresVectorStoreConfig`, `FileKnowledgeLoaderConfig`, `HTTPKnowledgeLoaderConfig`, `KnowledgeIndexerConfig`, `NewRetrieverTool`, `NewPostgresVectorStore`, `NewFileKnowledgeLoader`, `NewHTTPKnowledgeLoader`, `NewScoreReranker`, `NewLLMReranker`, `NewKnowledgeIndexer` | `github.com/aijustin/agentflow-go/pkg/adapters` |
  | MCP: `NewMCPHTTPClient`, `MCPStdioClientConfig`, `MCPStdioClient`, `NewMCPStdioClient`, `NewMCPToolExecutor` | `github.com/aijustin/agentflow-go/pkg/adapters` |
  | Tool executors: `HTTPToolConfig`, `FilesystemToolConfig`, `SQLToolConfig`, `GitToolConfig`, `TicketToolConfig`, `NewHTTPToolExecutor`, `NewFilesystemToolExecutor`, `NewSQLToolExecutor`, `NewGitToolExecutor`, `NewTicketToolExecutor`, `NewMemoryTicketStore`, `Ticket`, `TicketStore`, `ToolResolver`, `ToolResolverFunc` | `github.com/aijustin/agentflow-go/pkg/adapters` |
  | Tiered memory: `PostgresTierWarmStoreConfig`, `NewPostgresTierWarmStore`, `NewFileTierColdStore`, `BlobTierColdStoreConfig`, `NewBlobTierColdStore`, `TierColdSummaryIndexerConfig`, `NewTierColdSummaryIndexer`, `CompositeTierStoreConfig`, `NewCompositeTierStore`, `NewInMemoryTierHotStore`, `NewLLMTierSummarizer`, `NewCognitiveTierMemory` | `github.com/aijustin/agentflow-go/pkg/adapters` |
  | Observability: `NewSlogEventSink`, `NewVerboseSlogEventSink`, `NewSlogAuditSink`, `NewObservabilityEventSink`, `NewNoopAuditSink`, `NewInMemoryAuditSink`, `NewFileAuditSink`, `PostgresEventStoreConfig`, `NewInMemoryEventStore`, `NewPostgresEventStore`, `NewEventHub`, `NewEventStoreSink`, `NewEventFanoutSink`, `PrometheusRecorder`, `NewPrometheusRecorder`, `PrometheusMetricsHandler`, `OpenTelemetryTracer`, `OpenTelemetryTracerProviderConfig`, `NewOpenTelemetryTracer`, `NewOpenTelemetryStdoutTracerProvider`, `OpenTelemetryTracerFromProvider` | `github.com/aijustin/agentflow-go/pkg/adapters` |
  | Mock LLM gateway: `NewMockLLMGateway` | `github.com/aijustin/agentflow-go/pkg/testutil` |

- `pkg/adapters` never imports the root facade (verified: `go list -deps ./pkg/adapters` contains no root package), so applications that only need these constructors can depend on it alone.

## [Unreleased]

### Added

- **Deferred tool catalog (`pkg/toolcatalog`)**: keyword `Search`, schema `Load`, version/TTL snapshot metadata, and built-in meta-tools `search_tools` / `load_tool_schemas`. Wire with `WithToolCatalog`; deferred mode is on by default when a catalog is attached (`WithDeferredTools` to override).
- **`runstate.ErrFenceRequired`**: leased run saves now hard-fail when the repository does not implement `FencedRepository` instead of warn-and-continue plain `Save`.
- **Observation masking**: `contextwindow.Policy.ObservationMaskAfterTurns` masks stale tool results before summarization; `contextwindow.CompactContext` drops masked tool messages.
- **Tracing spans**: `agentflow.context.prepare`, `agentflow.mcp.rpc` (constant for platform-owned MCP RPC).
- **MCP forward-compat**: optional `ttlMs` / `cacheScope` on `mcp.ListToolsResult`.

### Changed

- **BREAKING — leased runs without fencing support**: repositories that do not implement `FencedRepository` reject fenced saves with `ErrFenceRequired` when a run lease stamps a non-zero fence token. Multi-node deployments must use a fencing-capable run-state backend (Postgres/inmem fenced repos).

## [0.4.1] - 2026-07-25

### Fixed

- Restore the aggregate coverage gate above 80% after ComposeGraph growth by covering compose edit tools (`set_input` / `disconnect` / `remove_node` / `add_skill`), `StudioParts`, `ValidateWiringWithOptions`, JWT middleware construction, and composer LLM resolution.

### Changed

- Align README / HTML manual / persistence docs / Helm reference versions with v0.4 adapters/`httpx` package paths, ComposeGraph, and the AI-first Studio SPA.

## [0.4.0] - 2026-07-25

### Added

- **Studio SPA rewrite (`web/studio/`)**: the observability dashboard gets a new AI-first frontend — a Vite + React + TypeScript + Tailwind + React Flow single-page app, `go:embed`-ed into the binary (single-binary distribution unchanged). Build view: AI compose bar wired to `POST /api/studio/compose` (catalog/scenario), parts palette (`GET /api/studio/parts`) with drag-to-add nodes, canvas editing with undo/redo and subgraph drill-down, node inspector, validate/YAML/Go-codegen/save actions, and a trial-run panel with SSE node highlighting and HITL approve/deny. Runs view (trace span tree, step outputs, checkpoint time-travel with resume/fork, thread lineage) and Compare view (step-level diff) carry over the old dashboard's capabilities. Serving is adaptive: the SPA is served when the bundle exists (`make studio-ui`), otherwise the legacy inline UI remains as the zero-Node fallback — `go build` never requires Node.js.
- **Studio HTTP API additions**: `POST /api/studio/compose` + `GET /api/studio/parts` on the observability handler, mirrored as `POST /v1/studio/compose` + `GET /v1/studio/parts` on the production Studio handler (compose is a mutating endpoint: default-deny without `AuthMiddleware`/`Policy`, `run.submit` action). Root API gains `Studio.Parts()` / `Framework.StudioParts()`. The `http-worker` example now sets `InsecureAllowNoAuth` so the dashboard's mutating endpoints work in the local demo, and queues mock composer turns so AI composition can be tried without a real LLM.

- **AI graph composition (`Studio.ComposeGraph` / `Framework.ComposeGraph`)**: a natural-language task is turned into a validated scenario graph by an agentic composer — an internal agent builds the draft through `compose_*` tools (`list_parts` / `add_node` / `connect` / `validate` / `finish`, plus `add_agent` / `add_skill` in scenario mode) with incremental validation feedback instead of single-shot JSON generation. Two modes: `catalog` (default) orchestrates only registered parts; `scenario` adds new agents/prompt-skills as an additive patch that rejects overwriting existing part IDs. The draft is merged onto a deep copy of the live scenario, fully validated (`ValidateScenario`), and optionally executed ephemerally — catalog runs reuse `RunStudioGraph`, scenario runs execute on a temporary engine/workflow-runner pair (fixed_workflow only) so new agents resolve correctly. Live framework state is never mutated; persistence stays explicit (`SaveStudioGraph`). Supporting pieces: `graph.ScenarioPatch` + `graph.ApplyScenarioPatch` + `graph.DeepCopyScenario` (`pkg/graph/patch.go`), `builder.MinimalGraphComposer` base preset, and `examples/go/compose-graph`.

- **Single durable tier for session memory**: a one-level store (e.g. the Postgres tier store) can now back a tier manager directly — no composite, no reconcile queue. The new `tier.SingleLevelPolicy()` disables promotion/demotion/TTL/capacity eviction so recall bookkeeping never migrates records out of the durable tier; the store forces its own level on `Put` and answers `List`/`Count` for other levels with empty results, so `Remember` persists immediately and survives restarts. New `tier.MessageRecord(ns, ChatMessage, ...SeedOption)` (with `WithProvenance`, e.g. `memory.ProvenanceChatHydrate`) seeds chat history with the exact field-population rules the runtime uses, keeping host-seeded and framework-written records indistinguishable to recall scoring. The `pkg/memory/tier` package doc now explains how to approximate a flat repository's "most recent, replayed in order" recall (empty query + recency-dominant weights + large budget).
- **`Framework.ContinueRun(ctx, runID)`**: public idempotent recovery entry point for runs stuck in `Running` with unconsumed checkpoint metadata (gate approved but the continue failed or the worker crashed). A `Completed` run returns its persisted result; other states return classified errors.
- **Idempotent resume + `ErrResumeInProgress`**: `ResumeAndContinue`/`ResumeRunByID` on an already-`Completed` run return the persisted `RunResult` instead of a token error; a concurrent resume of the same run (with or without run leases) fails fast with the new `ErrResumeInProgress` sentinel instead of racing the pause token into an ambiguous `ErrTokenSuperseded`. Token verification now reports `ErrTokenExpired` (wrapping `ErrInvalidToken`) for expired tokens.
- **Detached streaming**: `WithStreamDetached()` (StreamRun) and `StreamDetached(ctx)` (Stream) keep a run executing to a terminal state in the background when the caller's context is cancelled (client disconnect), instead of marking it `Cancelled`.
- **StreamRun event fanout**: workflow/hybrid node events and facade lifecycle emissions now reach the StreamRun frame stream via a Framework-level tee sink; dropped teed events surface as `StreamFrameEventsLost` marker frames with the cumulative count, and `EventHub.DroppedEvents()` exposes the hub-level drop counter.
- **`RetryFailedRun` supports autonomous runs** with pending checkpoint metadata (continues via `ContinueAfterCheckpoint`); runs without one get an explicit error.
- **`runstate.CheckpointHistory.Delete`**: retention purges (`PurgeRuns`, `PurgeExpired`) now drop a run's checkpoint revisions alongside its snapshot; `PurgeRuns` skips non-terminal runs unless the new `WithPurgeForce()` option is given.
- **HTTP authorization for mutating endpoints**: the checkpoint handler (`CheckpointHTTPHandlerConfig.Policy`), Studio handler (`StudioHTTPHandlerConfig.Policy`), observability dashboard (`ObservabilityHTTPHandlerConfig.AuthMiddleware`, mutating routes guarded when absent), and retention handler now authorize writes via `security.Policy` exactly like the async run handler; webhook ingress gained `WebhookHTTPHandlerConfig.VerifySignature` for HMAC-style body validation. `NewProductionHTTPHandler` threads its `Policy`/`Audit` into the checkpoint, studio, and retention sub-handlers.
- **Tenant-strict mode**: `runstate.ContextWithTenantStrictMode(ctx)` makes `AuthorizeTenant`/`LoadAuthorized` fail closed when the caller has no tenant principal (`ErrTenantRequired`) or the snapshot predates tenant stamping (`ErrTenantMismatch`). Off by default; multi-tenant deployments should enable it in auth middleware.
- **HITL token rotation**: `runstate.NewTokenSignerWithRotation(primary, secondary)` signs with the primary key (embedding a key id) and verifies with both, so key swaps do not invalidate in-flight tokens; facade option `WithHITLTokenRotation(primary, secondary, tokenWriter)`. `runstate.MinTokenSecretLength` (16) is now enforced for every signer.
- **`WithResumeAuthorizationHook`**: gates `ResumeRunByID` before it mints a fresh resume token.
- **`WithRunReaper(interval, gracePeriod)`**: opt-in background loop that periodically calls `MarkAbandonedRuns`, so a crashed worker's `Running` runs are failed automatically instead of waiting for an operator. The sweep is lease-probe based and idempotent, so any number of nodes may run it concurrently; it requires `WithRunLease` and stops on `Framework.Close`. Multi-node deployments should enable it on every worker.
- **Zombie-run self-heal in the async run handler**: a redelivered run job that finds its run still `Running` (`ErrRunInProgress`) now probes the run lease; an unheld lease proves the original worker is gone, so the run is marked abandoned and re-entered through `RetryFailedRun` instead of dead-lettering the job and stranding the run. A live lease holder keeps the previous fail-and-redeliver semantics. Without `WithRunLease` the handler keeps the old behavior and logs a one-time warning; `ValidateWiring`/`New` also warn (without failing) when a job queue and shared run-state repository are configured without `WithRunLease`.
- **`async.LeaseReleaser`**: optional queue capability (`Release`) implemented by the in-memory and Postgres queues, returning a leased job to the queued state without counting a failure.

### Fixed

- **Lease-lost classification**: a run aborted by a lost lease (`ErrRunLeaseLost`) is persisted as `Failed` with the lease-lost reason in `run_error_message`, never as `Cancelled`; the facade and `pkg/coordination` now share one sentinel. `MarkAbandonedRuns` only reaps `Running` runs stamped with a lease owner, so workers without lease coordination are no longer mistaken for zombies.
- **Permanent continue errors mark Failed**: resume/continue failures that can never succeed on a blind retry — missing LLM gateway (new `ErrLLMGatewayRequired` sentinel, wrapped by both plain and structured answer paths), corrupt or already-consumed checkpoint metadata, and agent/profile validation failures inside `continueToolApproval` — now persist the run as `Failed` with the reason in `run_error_message`, instead of leaving it stranded in `Running` until a reaper gives up. Checkpoint variables are intentionally kept, so `RetryFailedRun` (autonomous, with checkpoint metadata) recovers the run once the configuration is fixed. Transient failures (provider 429/5xx, network, timeouts) keep the existing `Running` + checkpoint semantics for `ContinueRun`.
- **Parallel tool batch CAS storm**: parallel tool batches no longer race N optimistic-CAS writers on the same run snapshot. Tool I/O still runs in parallel, but results persist in a single `saveStepOutputs` round (one Load, one Save, stale-retry only), so tool results stop failing with spurious `persist tool output: ... after stale snapshot retries` (which misled models into same-input retries and tripped the doom-loop guard). A genuine batch persist failure is still annotated on the affected results and surfaced via `persist_error` events and audit.
- **Reject with continue**: `ResumeRunByID(reject, continueExecution=true)` restores the legacy reject semantics (run marked Cancelled, terminal result returned) instead of entering `continueRun` against the just-cancelled snapshot.
- **Streaming retry classification**: mid-stream provider errors keep their structured type (`llm.ChatChunk.Err`, populated by the OpenAI/Anthropic gateways including in-stream error payloads), so `shouldRetry` works on the streaming tool-loop path.
- **Lifecycle event durability**: `RunCompleted`/`RunPaused`/`RunFailed`/`RunCancelled` emissions retry with backoff (3 attempts) before an error-level log; terminal transitions clear run-scoped approval cache, deny-breaker counters, and interjection buffers; `Interject` rejects unknown or inactive runs.
- **Pause/commit edges**: `ensureRunPaused` failures propagate instead of being warn-only; a `completionConflict` during continue clears leftover checkpoint variables; async workers back off on lease poll failures, cancel jobs whose lease renewal fails, and retry queue failures with exponential backoff (postgres `available_at`); `handleRun` re-enters failed runs via `RetryFailedRun` instead of re-running from scratch.
- **HTTP error semantics**: human-gate resume maps errors to 401/410/409/500 with structured `error_code`; event-router and checkpoint handlers distinguish 400/404/409/500 the same way; `NewHumanHTTPHandler` validates a nil framework at construction.
- **Run ID entropy**: all generators delegate to `runstate.GenerateRunID` (128-bit); stale-snapshot retry loops back off with jitter.

### Security

- Bump `golang.org/x/text` to v0.39.0 (GO-2026-5970: infinite loop on invalid UTF-8 in `norm`).
- **BREAKING (intentional hardening)**: checkpoint write endpoints (`resume-from-step`, `resume-from-checkpoint`, `fork`), Studio `run`/`save`, observability mutating endpoints (HITL resume, resume-from-step/checkpoint, fork, studio run/save), and retention purge endpoints now **default-deny with 403 `auth_required`** when no authorization policy / `AuthMiddleware` is configured. Set the corresponding `Policy` (or `AuthMiddleware`) to authorize them, or explicitly opt out with the new `InsecureAllowNoAuth` field (intended only for tests or deployments behind an authenticating proxy). Read-only endpoints stay open; the observability handler logs a one-time construction warning when no `AuthMiddleware` is set.
- **BREAKING (intentional hardening)**: `runstate.NewTokenSigner` rejects secrets shorter than 16 bytes. Single-key signers keep the legacy two-segment token format so rolling upgrades do not mix wire formats.
- `ResumeRunByID` is documented as an indefinite resume capability not bounded by the token TTL; HTTP exposures must authorize it (e.g. with `WithResumeAuthorizationHook`).

### Fixed

- **HITL pause correctness**: workflow node retries, `parallel_group`/`map` `collect_errors`, and parallel batches no longer swallow or re-run a `WorkflowPausedError`; `RunHybrid`/`RunStructured` map pauses to a paused result instead of marking the run failed; `errorsAsRunPaused` now uses `errors.As` so wrapped pauses are detected.
- **Hybrid context**: caller-supplied `Context` is merged with hydrated workflow step outputs instead of dropping the workflow results.
- **Tiered memory durability**: `CompositeStore.Put` writes the destination tier before removing stale copies (no data loss on failed migration); cold file store now locks `Put`/`Get`/`Delete` consistently; file/blob/cold/blob-cold stores fsync data and the parent directory before/after atomic rename; `blobcold.Put` persists the index before deleting the previous blob.
- **`WithTierStore` policy** is now applied to the derived tier manager (previously ignored).
- **Checkpoint history**: recording repository appends the snapshot it just saved instead of re-loading (avoids version skew under concurrency).
- **Streaming lifecycle**: OpenAI/Anthropic gateways and the runtime stream forwarder send through a context-aware `select`, so an abandoned stream no longer leaks the goroutine or the HTTP response body.
- **Dual-write memory**: tier (source of truth) is written before the cognitive index mirror, and a mirror failure is wrapped to signal partial success for retry.
- **Orphan blob GC race**: `PurgeOrphanBlobs` now lists blobs before snapshots and re-checks references immediately before deletion, so a blob written concurrently with its referencing snapshot is never deleted while live.
- **Runtime step-output durability**: `Engine.saveStepOutput` retries on a stale snapshot (matching the orchestration path) so concurrent writers to the same run no longer lose a step output to an optimistic-concurrency conflict.
- **Tier reconcile concurrency**: `Reconcile` is serialized per namespace so overlapping passes no longer make tier-capacity decisions from each other's stale level counts.
- **Async worker shutdown**: a leased job whose worker context is cancelled now releases its lease via a detached context instead of leaving the job leased until expiry.
- **Subgraph aggregation**: externalized (blob-stored) child step outputs are hydrated back into the aggregated subgraph result instead of being replaced by an opaque `{"external":true}` marker that downstream nodes could not read.

### Added

- **Redis run-state connection pooling**: the Redis repository keeps a bounded pool of authenticated connections (configurable via `MaxIdleConns`, default 8), performing the `AUTH`/`SELECT` handshake once per connection and validating liveness before reuse; added `Close()` to drain idle connections on shutdown.
- **Cognitive index rebuild**: tier managers expose `RecordEnumerator.ListAll` and `IndexRebuilder.RebuildIndex`, so a cognitive search index that fell behind after a transient mirror failure can be reconciled from the source-of-truth tier store.

## [0.2.2] - 2026-05-22

### Added

- Editor **live run preview**: Studio Run stays on Editor with done/current node highlighting during SSE updates.
- Editor **subgraph drill-down**: double-click subgraph nodes, property-panel button, breadcrumb back; scoped step highlighting when drilled.
- Inspector **trace/span tree** with optional external trace link (`TraceExploreURL` / `GET /api/ui-config`).
- Event `parent_span_id` propagation for nested OTel spans.

## [0.2.1] - 2026-05-22

### Added

- Observability Graph **node inspector**: step output, related workflow events, Timeline ↔ Graph `node_id` linkage.
- Checkpoint **timeline scrub**, revision step diff, and **fork from checkpoint** in Time Travel bar.
- **Autonomous trace** panel under Graph for hybrid/autonomous LLM and tool events (P9.2).
- Workflow events from agent/tool nodes include **`node_id`** in payload for Timeline ↔ Graph linkage (P9.2).
- Builder workflow DSL: `StepPath`, `ConditionEq/Ne/Exists/Missing`, `MapOver`, `Map*Branch`, `RouteIf` (P8).
- Studio **P10**: live Graph refresh during runs, subgraph drill-down, inspector event payloads, compare step output diff.
- Builder DSL sugar: `ParallelGroup`, `ParallelTools`, `RouteIfNe/Exists/Missing`; codegen emits `MapOver`/`RouteIf` when possible.
- Helm reference chart: production defaults (probes, resources, securityContext).
- Cross-process run-state integration test.

## [0.2.0] - 2026-05-22

### Removed

- Public YAML scenario loaders: `LoadScenarioFile`, `LoadScenario`, `NewFromFile`. Use `pkg/builder` or `core.Scenario`; Studio import/export APIs remain.
- `examples/go/validate -kind scenario` and `testutil.ScenarioWorkDir`.

### Changed

- **Breaking:** builder-first embed path; scenario YAML is Studio interchange only. Migration: replace `NewFromFile` / `LoadScenarioFile` with `builder.*` stacks and `agentflow.New(scenario, opts...)`. See [docs/library-integration.md](docs/library-integration.md#migrating-from-v01-yaml-loaders).
- README YAML sections condensed; field details live in `docs/configuration-reference.md`.
- `docs/superpowers/plans/*` marked as historical implementation records.

### Fixed

- `ImportStudioScenarioYAML` nil panic when `layout.Workflow` is omitted.

## [0.1.10] - 2026-05-22

### Added

- `pkg/builder` fluent Go DSL for constructing `core.Scenario` without YAML; 18 stack functions aligned with `examples/*.yaml`.
- Root package re-exports for common builder stacks and workflows (`builder.go`).
- `examples/go/validate -kind builder` and `make validate-builder` for catalog validation in CI (`release-check`).
- [docs/builder-reference.md](docs/builder-reference.md) and Go DSL section in [docs/manual.html](docs/manual.html).

### Fixed

- golangci-lint: gofmt on `pkg/builder`, remove unused dead code, fix ineffectual stream assignments in LLM gateways.

## [0.1.9] - 2026-05-22

### Added

- Memory tier runtime, cognitive recall, tier-worker reference deploy, and related documentation.

### Changed

- `release-check` runs govulncheck via `go run`; OpenTelemetry bumped to v1.40.0.

## Earlier v0.1.x (aggregated)

### Changed

- **Library-only positioning:** removed `cmd/agentctl`, `cmd/agent-http`, `cmd/agent-server`, `cmd/agent-worker`, debug UI, and `deploy/` templates. Integrate via `go get` and `examples/go/*`.
- Moved test/example wiring to `pkg/testutil` (`WiringOptions`). Removed `DemoOptions`, `DevelopmentOptions`, and env-based `NewProduction*` helpers from the root package.
- PostgreSQL migrations live under `migrations/postgres/` (was `deploy/migrations/postgres/`).
- Release workflow runs `make release-check` only (no binary artifacts).

### Added

- Memory tier design spec and Phase 1 `pkg/memory/tier` contract (hot/warm/cold policy, recall budget). See `docs/superpowers/specs/2026-05-21-memory-tier-design.md`.
- Memory tier Phase 2: YAML `memories.*.tiers`, `TierManager` Remember/Recall/Reconcile, in-memory tier store, `WithTierMemory` / `WithTierStore`, runtime tier read/write path, migration event types, and `examples/tier_memory.yaml`.
- Memory tier Phase 3: Postgres warm store, gzip file cold store, `CompositeStore`, `WithJobQueue` + `memory.reconcile` async job, tier Prometheus metrics and OTel spans, tenant-scoped memory namespaces.
- Memory tier Phase 4: cognitive/tier unified recall (`RankMemories` + tier budget), `DualWriteManager`, `CognitiveAdapter`, `WithCognitiveMemory`, runtime query-aware tier recall.
- M6 reference deploy: `examples/go/tier-worker`, migration init script, Kubernetes and Helm skeletons, `memory.reconcile` via shared Postgres/in-memory job queue.
- `examples/go/validate` for scenario YAML wiring checks in CI and local dev; supports `-kind tool|skill` for catalog manifests.
- `NewPrometheusRecorder` and `PrometheusMetricsHandler` with `/metrics` mounting on `NewProductionHTTPHandler`.
- OpenTelemetry adapter in `pkg/observability/otel`: `NewOpenTelemetryTracer`, `NewOpenTelemetryStdoutTracerProvider`, runtime `Run`/`ToolCall` spans.
- Tool/Skill catalog manifest loaders: `LoadToolManifestFile`, `LoadSkillManifestFile`, `ValidateToolManifest`, `ValidateSkillManifest`.
- Reference local stack under `examples/deploy/` (PostgreSQL, Redis, MinIO Compose).
- ADR documenting library-first integration: [docs/adr/001-library-first.md](docs/adr/001-library-first.md).
- `NewMockLLMGateway` remains on the root package; demo tool wiring is in `pkg/testutil`.
- Workflow dynamic edge condition routing at runtime (`edges[].condition`).
- Workflow node input templates: `copy_from` and `prompt_from` in node `input`.
- `runstate.HydrateStepContext` for hybrid Phase 2 workflow context hydration.
- Context window `compression.trigger_ratio` pre-compression for tool messages.
- `memory_recall_limit` enforcement when replaying session memory into LLM context.
- Workflow step output redaction via `WithOutputRedactor` on `WorkflowRunner`.
- Memory write redaction using the configured `OutputRedactor`.
- `Framework.ResumeAndContinue` for continuing paused autonomous, workflow, and tool-approval runs after HITL approval.
- Tool approval policy `pause` for pausing before risky tool execution and resuming with `ResumeAndContinue`.
- Workflow nodes `parallel_group` and `loop` for multi-agent parallelism and bounded iteration.
- Built-in Git and ticket tool executors (`NewGitToolExecutor`, `NewTicketToolExecutor`).
- Planning pass execution tracking during autonomous runs.
- Event routing via `scenario.triggers`, `Framework.HandleEvent`, and `NewWebhookHTTPHandler`. Use `examples/go/event-trigger` or host HTTP `POST /v1/events` instead of removed `agentctl trigger`.
- Production HTTP handler sync routes `POST /v1/events` and `POST /v1/hitl/resume` when `Framework` is configured.
- Async job types `event` and `resume.continue` with HTTP enqueue routes `POST /v1/jobs/events` and `POST /v1/jobs/hitl/resume`.
- `NewFrameworkJobHandler` composite worker handler.
- HTTP HITL `continue: true` for `ResumeAndContinue`. Use `examples/go/hitl-resume` or `POST /v1/hitl/resume` instead of removed `agentctl resume --continue`.
- Example scenarios: `ticket_handling.yaml`, `code_review_pipeline.yaml`, `multi_expert_research.yaml`.

## [0.1.0] - 2026-05-17

### Added

- Public packages for core agent concepts, LLM gateway contracts, memory, and run state.
- Root facade package for using the project as an importable Go framework.
- Root facade constructors for OpenAI-compatible, Anthropic, local, and routed LLM gateways.
- YAML scenario loader and validator.
- In-memory memory, run-state, and blob adapters.
- CLI commands for `validate`, `run`, `resume`, and `version`.
- Durable CLI run/resume state through `--state-dir`, plus expiring HITL tokens with `--token-ttl`.
- HTTP/Webhook resume handler for human-in-the-loop flows.
- Safer debug HTTP startup defaults: non-loopback listeners require `AGENT_TOKEN_SECRET`.
- Browser debug console for running built-in scenarios, editing YAML, observing event timelines, inspecting run state, and testing real local-model calls.
- Open-source project scaffolding: Apache-2.0 license, GitHub Actions CI, golangci-lint config, govulncheck/CodeQL security workflows, Dependabot, GoReleaser config, Dockerfile, SECURITY.md, and CODE_OF_CONDUCT.md.
- Context governance package and runtime wiring for sliding-window trimming, summary compression, and `ContextPrepared` observability events.
- Richer LLM profile configuration, including context window size, output budget, temperature, top-p, thinking budget, reasoning effort, timeouts, and provider-specific `extra_body` fields.
- Autonomous tool-calling runtime loop with agent tool whitelist checks, approval-policy denial, tool dispatch, tool-result feedback to the LLM, `max_steps` enforcement, and LLM/tool lifecycle events.
- Lazy tool resolution through `core.ToolResolver` and root `WithToolResolver`, allowing heavy or tenant-scoped tool executors to be bound only after allowlist, approval, RBAC, governance, and rate-cap checks pass.
- OpenAI-compatible function-calling request/response support plus router/mock propagation for `ToolCaller`.
- Runtime memory integration for agent-bound conversation/session history, including memory injection before context preparation and writes for user prompts, assistant answers, and tool observations.
- Fixed-workflow scheduler for graph dependencies, `depends_on`, edge conditions, bounded parallel batches, retry policy, transform nodes, agent nodes, human-gate nodes, and CAS-safe parallel step output persistence.
- Root facade execution for `fixed_workflow` scenarios.
- Workflow-level HITL pause/resume support: human-gate nodes persist `CurrentNodeID`/`PendingGate`, return a typed pause error with token, and resume downstream graph execution after approval.
- Multi-agent delegation baseline: `sub_agents` are exposed as virtual delegation tools in the autonomous loop, supervisor agents can call sub-agents, and delegated outputs are persisted and fed back to the supervisor.
- Skill workflow expansion during scenario build: skill prompt fragments merge into agent instructions and skill workflow nodes/edges are namespaced into the parent workflow.
- File-backed durable adapters for RunStateRepository, BlobStore, and MemoryRepository, exposed through the root facade.
- PostgreSQL-compatible RunStateRepository exposed through the root facade without forcing a specific database driver dependency.
- S3-compatible BlobStore exposed through the root facade with standard-library AWS Signature Version 4 signing.
- Redis-backed distributed lease adapter exposed through the root facade for worker and workflow coordination.
- Async job queue and worker contracts with an in-memory queue adapter for local development and tests.
- Framework run job payload and worker handler for executing queued `Framework.Run` jobs.
- PostgreSQL async job queue adapter exposed through `NewPostgresJobQueue`.
- Production HTTP handler with `/healthz`, `/readyz`, and async `/v1/runs` submit/status/cancel routing.
- Enterprise identity context, API key middleware, and RBAC policy contracts.
- Audit event model plus noop, in-memory, and JSONL file audit sinks.
- Structured `slog` runtime event and audit sinks.
- Governance package with tool budget, tool side-effect policy, policy chaining, and JSON field output redaction.
- Authorization middleware and framework security/audit options for runtime tool invocation enforcement.
- Async run HTTP submit/status/cancel handler with RBAC and audit wiring.
- Optional API key protection for `agent-http`; non-loopback listeners now require `AGENT_HTTP_API_KEY` in addition to `AGENT_TOKEN_SECRET`.
- Stricter scenario validation for memory type/scope, tool approval/side-effect policies, workflow `depends_on`, node kinds, and node references.
- Production hardening baseline: runtime/agent/profile timeouts, context-aware LLM and tool retries, per-run tool rate caps, failed-run status persistence, and BlobStore externalization for large runtime/workflow outputs.
- Structured output and streaming runtime paths, exposed through the root facade and wired through mock/router/OpenAI-compatible adapters.
- Provider capability helpers for parsing and checking explicit profile capabilities.
- MCP public client contracts plus HTTP JSON-RPC client and MCP tool executor adapters.
- Knowledge/RAG foundation with public vector-store contracts, OpenAI-compatible embedding support, mock embedding queues, pgvector baseline adapter, and retriever tool.
- Knowledge ingestion pipeline with filesystem and HTTP loading, text chunking, batch embedding/upsert indexing, and explicit retriever citations.
- Root facade constructors for MCP clients/executors, OpenAI-compatible chat+embedding providers, retriever tools, and PostgreSQL vector stores.
- Local enterprise deployment template with PostgreSQL+pgvector, Redis, MinIO, agent-http, bootstrap SQL, reusable PostgreSQL migrations, and an initial Kustomize base.
- Constrained built-in HTTP, filesystem read, and SQL query tool executors with allowlists, size/row limits, timeouts, root facade constructors, and SQL validator compatibility for PostgreSQL, MySQL, and ClickHouse-style read-only queries.
- Release validation target and v0 API stability policy, including public surface, migration notes, and release checklist documentation.
- Public module, documentation, import examples, and deployment image references now target `github.com/aijustin/agentflow-go`.
- MCP and RAG example scenarios plus documentation for MCP tools, knowledge retrieval, and pgvector persistence.
- LLM adapters for OpenAI-compatible APIs, Anthropic, local OpenAI-compatible endpoints, and mock testing.
- Unit and integration tests covering configuration, runtime, HITL, run state, memory, LLM routing, and workflow execution.
- Environment-driven `realmodel` integration tests for OpenAI-compatible local model endpoints, including long-context governance with a real model.

### Fixed

- Fixed-workflow `agent` nodes now execute through the runtime-backed agent path instead of saving a dummy completion output, so LLM calls, memory, tools, and observability events are preserved.

### Known limitations

- Autonomous planning beyond tool-calling loops is still incomplete.
- Redis run-state storage adapter beyond lease coordination is still pending.
- Prometheus metrics and OpenTelemetry tracing adapters are still pending.
- Specialized ingestion connectors, Helm chart packaging, and additional built-in enterprise tool packages for chatops integrations are still pending.

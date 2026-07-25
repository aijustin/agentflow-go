// API types mirroring the agentflow Go JSON shapes.

export interface GraphPosition {
  x: number;
  y: number;
}

export interface GraphNode {
  id: string;
  kind: string;
  ref?: string;
  input?: unknown;
  condition?: string;
  depends_on?: string[];
  interrupt?: boolean;
  resumable?: boolean;
  resume_hint?: string;
}

export interface GraphEdge {
  from: string;
  to: string;
  condition?: string;
}

export interface GraphView {
  id?: string;
  nodes: GraphNode[];
  edges: GraphEdge[];
  layout?: Record<string, GraphPosition>;
}

export interface ScenarioGraph {
  name: string;
  mode: string;
  workflow?: GraphView;
  workflows?: Record<string, GraphView>;
}

export interface StudioPart {
  name: string;
  description?: string;
}

export interface StudioParts {
  agents: StudioPart[];
  tools: StudioPart[];
  skills: StudioPart[];
  subgraphs: StudioPart[];
}

export type RunStatus = 'running' | 'paused' | 'failed' | 'completed' | 'cancelled';

export interface RunResult {
  run_id: string;
  status: RunStatus;
  token?: string;
  output?: string;
  structured_output?: unknown;
}

export interface ComposeGraphResult {
  mode: string;
  graph: ScenarioGraph;
  valid: boolean;
  error?: string;
  run?: RunResult;
}

export interface ValidateStudioResult {
  valid: boolean;
  error?: string;
  error_code?: string;
  scenario_name: string;
}

export interface CodegenResult {
  language: string;
  code: string;
}

export interface ImportStudioResult {
  scenario_name: string;
  graph: ScenarioGraph;
}

export interface RunSummary {
  run_id: string;
  scenario_name?: string;
  status: RunStatus;
  event_count: number;
  first_seen_at: string;
  last_seen_at: string;
  last_event_type?: string;
}

export interface CoreEvent {
  type: string;
  run_id: string;
  scenario_name?: string;
  timestamp: string;
  trace_id?: string;
  span_id?: string;
  parent_span_id?: string;
  category?: string;
  display_label?: string;
  payload?: unknown;
}

export interface EventRecord {
  id: number;
  sequence: number;
  event: CoreEvent;
  created_at: string;
}

export interface StepOutputRef {
  inline?: unknown;
  blob?: { id: string; size: number; sha256: string };
}

export interface RunStepsResult {
  run_id: string;
  version: number;
  status: RunStatus;
  current_node_id?: string;
  pending_hitl?: { node_id?: string; interrupt?: boolean };
  steps: { node_id: string; output: StepOutputRef }[];
}

export interface CheckpointSummary {
  run_id: string;
  version: number;
  status: RunStatus;
  current_node_id?: string;
  step_count: number;
  recorded_at: string;
}

export interface RunCheckpointsResult {
  run_id: string;
  checkpoints: CheckpointSummary[];
}

export interface RunSnapshot {
  run_id: string;
  version: number;
  scenario_name: string;
  status: RunStatus;
  current_node_id?: string;
  variables?: Record<string, unknown>;
  step_outputs?: Record<string, StepOutputRef>;
  created_at?: string;
  updated_at?: string;
}

export interface ThreadRun {
  run_id: string;
  parent_run_id?: string;
  fork_from_version?: number;
  thread_id: string;
  status: RunStatus;
  scenario_name?: string;
}

export interface RunCompareResult {
  run_a: string;
  run_b: string;
  status_a: RunStatus;
  status_b: RunStatus;
  steps_only_a: string[];
  steps_only_b: string[];
  shared_steps: { node_id: string; same: boolean; output_a?: StepOutputRef; output_b?: StepOutputRef }[];
}

export interface ApiError {
  error?: { code?: string; message?: string } | string;
  error_code?: string;
}

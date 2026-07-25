import { useQuery } from '@tanstack/react-query';
import { apiGet } from './client';
import type {
  RunCheckpointsResult,
  RunCompareResult,
  RunStepsResult,
  RunSummary,
  ScenarioGraph,
  StudioParts,
  ThreadRun,
} from './types';

export function useGraph() {
  return useQuery({
    queryKey: ['graph'],
    queryFn: () => apiGet<ScenarioGraph>('graph'),
  });
}

export function useParts() {
  return useQuery({
    queryKey: ['parts'],
    queryFn: () => apiGet<StudioParts>('studio/parts'),
  });
}

export function useRuns(status?: string) {
  return useQuery({
    queryKey: ['runs', status ?? ''],
    queryFn: () => apiGet<{ runs: RunSummary[] }>(`runs?limit=100${status ? `&status=${status}` : ''}`),
    refetchInterval: 5000,
  });
}

export function useRunSteps(runID: string | undefined) {
  return useQuery({
    queryKey: ['steps', runID],
    queryFn: () => apiGet<RunStepsResult>(`runs/${runID}/steps`),
    enabled: !!runID,
    refetchInterval: (query) => (query.state.data?.status === 'running' ? 3000 : false),
  });
}

export function useRunCheckpoints(runID: string | undefined) {
  return useQuery({
    queryKey: ['checkpoints', runID],
    queryFn: () => apiGet<RunCheckpointsResult>(`runs/${runID}/checkpoints`),
    enabled: !!runID,
  });
}

export function useRunThread(runID: string | undefined) {
  return useQuery({
    queryKey: ['thread', runID],
    queryFn: () => apiGet<{ thread_id: string; runs: ThreadRun[] }>(`runs/${runID}/thread`),
    enabled: !!runID,
  });
}

export function useCompare(runA: string | undefined, runB: string | undefined) {
  return useQuery({
    queryKey: ['compare', runA, runB],
    queryFn: () => apiGet<RunCompareResult>(`compare?run_a=${runA}&run_b=${runB}`),
    enabled: !!runA && !!runB,
  });
}

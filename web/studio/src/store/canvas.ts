import { create } from 'zustand';
import type { GraphView, ScenarioGraph } from '../api/types';

export type NodeRunState = 'running' | 'done' | 'failed';

interface CanvasState {
  /** The draft graph being edited. Initialized from GET /api/graph. */
  doc: ScenarioGraph | null;
  /** Drilled-into named workflow; null = main workflow. */
  subgraph: string | null;
  selectedNodeID: string | null;
  /** Draft diverges from the live scenario graph. */
  dirty: boolean;
  /** Full additive scenario returned by scenario-mode AI composition. */
  scenarioDraft: Record<string, unknown> | null;
  past: ScenarioGraph[];
  future: ScenarioGraph[];
  /** Trial-run / run-overlay: node ID → execution state (not undoable). */
  nodeRunState: Record<string, NodeRunState>;
  /** Run whose execution state is overlaid on the canvas (from run detail). */
  overlayRunID: string | null;

  setDoc: (doc: ScenarioGraph, dirty?: boolean, scenarioDraft?: Record<string, unknown> | null) => void;
  /** Apply a structural mutation with undo history. */
  mutate: (fn: (doc: ScenarioGraph) => void) => void;
  undo: () => void;
  redo: () => void;
  select: (id: string | null) => void;
  drill: (name: string | null) => void;
  setNodeRunState: (id: string, state: NodeRunState) => void;
  clearNodeRunState: () => void;
  setOverlayRun: (id: string | null) => void;
}

const HISTORY_CAP = 50;

export function currentView(state: Pick<CanvasState, 'doc' | 'subgraph'>): GraphView | undefined {
  if (!state.doc) return undefined;
  if (state.subgraph) return state.doc.workflows?.[state.subgraph];
  return state.doc.workflow;
}

export const useCanvasStore = create<CanvasState>((set) => ({
  doc: null,
  subgraph: null,
  selectedNodeID: null,
  dirty: false,
  scenarioDraft: null,
  past: [],
  future: [],
  nodeRunState: {},
  overlayRunID: null,

  setDoc: (doc, dirty = false, scenarioDraft = null) =>
    set({ doc, dirty, scenarioDraft, past: [], future: [], selectedNodeID: null, subgraph: null }),

  mutate: (fn) =>
    set((state) => {
      if (!state.doc) return state;
      const next = structuredClone(state.doc);
      fn(next);
      return {
        doc: next,
        dirty: true,
        past: [...state.past.slice(-HISTORY_CAP + 1), state.doc],
        future: [],
      };
    }),

  undo: () =>
    set((state) => {
      if (state.past.length === 0 || !state.doc) return state;
      const previous = state.past[state.past.length - 1];
      return {
        doc: previous,
        past: state.past.slice(0, -1),
        future: [state.doc, ...state.future],
        dirty: true,
        selectedNodeID: null,
      };
    }),

  redo: () =>
    set((state) => {
      if (state.future.length === 0 || !state.doc) return state;
      const [next, ...rest] = state.future;
      return {
        doc: next,
        past: [...state.past, state.doc],
        future: rest,
        dirty: true,
        selectedNodeID: null,
      };
    }),

  select: (id) => set({ selectedNodeID: id }),
  drill: (name) => set({ subgraph: name, selectedNodeID: null }),

  setNodeRunState: (id, nodeState) =>
    set((state) => ({ nodeRunState: { ...state.nodeRunState, [id]: nodeState } })),
  clearNodeRunState: () => set({ nodeRunState: {} }),
  setOverlayRun: (id) => set({ overlayRunID: id, nodeRunState: {} }),
}));

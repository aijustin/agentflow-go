import type { GraphEdge, GraphNode, GraphPosition } from '../api/types';

// layeredLayout computes simple left-to-right layered positions for nodes
// that lack saved layout: BFS depth over edges+depends_on, then stacking.
export function layeredLayout(
  nodes: GraphNode[],
  edges: GraphEdge[],
  saved: Record<string, GraphPosition> | undefined,
): Record<string, GraphPosition> {
  const depth = new Map<string, number>();
  const adjacency = new Map<string, string[]>();
  const indegree = new Map<string, number>();
  for (const node of nodes) {
    indegree.set(node.id, 0);
  }
  const link = (from: string, to: string) => {
    if (!indegree.has(from) || !indegree.has(to)) return;
    adjacency.set(from, [...(adjacency.get(from) ?? []), to]);
    indegree.set(to, (indegree.get(to) ?? 0) + 1);
  };
  for (const edge of edges) link(edge.from, edge.to);
  for (const node of nodes) for (const dep of node.depends_on ?? []) link(dep, node.id);

  const queue: string[] = [];
  for (const node of nodes) {
    if ((indegree.get(node.id) ?? 0) === 0) {
      depth.set(node.id, 0);
      queue.push(node.id);
    }
  }
  while (queue.length > 0) {
    const id = queue.shift()!;
    const d = depth.get(id) ?? 0;
    for (const next of adjacency.get(id) ?? []) {
      const best = Math.max(depth.get(next) ?? 0, d + 1);
      depth.set(next, best);
      indegree.set(next, (indegree.get(next) ?? 1) - 1);
      if ((indegree.get(next) ?? 0) <= 0) queue.push(next);
    }
  }

  const positions: Record<string, GraphPosition> = {};
  const layerCounts = new Map<number, number>();
  for (const node of nodes) {
    const d = depth.get(node.id) ?? 0;
    const index = layerCounts.get(d) ?? 0;
    layerCounts.set(d, index + 1);
    positions[node.id] = saved?.[node.id] ?? { x: 60 + d * 230, y: 60 + index * 110 };
  }
  return positions;
}

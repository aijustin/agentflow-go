package http

const indexHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>AgentFlow Observability</title>
  <style>
    :root {
      color-scheme: light;
      --bg: #f6f7f9;
      --panel: #ffffff;
      --panel-2: #eef2f6;
      --line: #d9e0e7;
      --text: #1e2732;
      --muted: #697586;
      --accent: #0f766e;
      --accent-2: #b45309;
      --danger: #b42318;
      --ok: #12805c;
      --shadow: 0 16px 40px rgba(31, 41, 55, 0.08);
      font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      background: var(--bg);
      color: var(--text);
      min-height: 100vh;
    }
    header {
      height: 58px;
      display: flex;
      align-items: center;
      justify-content: space-between;
      padding: 0 20px;
      border-bottom: 1px solid var(--line);
      background: rgba(255, 255, 255, 0.92);
      position: sticky;
      top: 0;
      z-index: 5;
      backdrop-filter: blur(12px);
    }
    h1 { font-size: 17px; margin: 0; letter-spacing: 0; }
    main {
      height: calc(100vh - 58px);
      display: grid;
      grid-template-columns: minmax(280px, 360px) minmax(420px, 1fr) minmax(320px, 420px);
      gap: 12px;
      padding: 12px;
    }
    button, select {
      border: 1px solid var(--line);
      background: #fff;
      color: var(--text);
      min-height: 34px;
      border-radius: 6px;
      padding: 0 10px;
      font: inherit;
    }
    button { cursor: pointer; }
    button.primary { background: var(--accent); border-color: var(--accent); color: #fff; }
    .toolbar { display: flex; gap: 8px; align-items: center; }
    .panel {
      min-height: 0;
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
      box-shadow: var(--shadow);
      display: flex;
      flex-direction: column;
      overflow: hidden;
    }
    .panel-head {
      min-height: 48px;
      padding: 10px 12px;
      border-bottom: 1px solid var(--line);
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 8px;
    }
    .panel-title { font-size: 13px; font-weight: 700; color: var(--text); }
    .list, .timeline, .detail-body { overflow: auto; min-height: 0; }
    .run {
      display: grid;
      gap: 6px;
      padding: 12px;
      border-bottom: 1px solid var(--line);
      cursor: pointer;
    }
    .run:hover, .run.active { background: #f0f7f6; }
    .run-main, .event-main { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
    .run-id, .event-type { font-weight: 700; font-size: 13px; overflow-wrap: anywhere; }
    .meta { color: var(--muted); font-size: 12px; display: flex; gap: 10px; flex-wrap: wrap; }
    .badge {
      display: inline-flex;
      align-items: center;
      height: 22px;
      border-radius: 999px;
      padding: 0 8px;
      font-size: 12px;
      font-weight: 700;
      border: 1px solid var(--line);
      background: var(--panel-2);
      white-space: nowrap;
    }
    .badge.running { color: var(--accent); background: #e7f6f3; border-color: #b8ded7; }
    .badge.completed { color: var(--ok); background: #e8f7ef; border-color: #bfe4cf; }
    .badge.failed { color: var(--danger); background: #fff0ed; border-color: #f0c4bb; }
    .badge.paused { color: var(--accent-2); background: #fff5e5; border-color: #efd4a8; }
    .event {
      display: grid;
      gap: 7px;
      padding: 12px 14px 12px 18px;
      border-bottom: 1px solid var(--line);
      position: relative;
      cursor: pointer;
    }
    .event::before {
      content: "";
      position: absolute;
      left: 7px;
      top: 12px;
      bottom: 12px;
      width: 3px;
      border-radius: 3px;
      background: var(--line);
    }
    .event.tool::before { background: var(--accent); }
    .event.llm::before { background: #2563eb; }
    .event.run::before { background: var(--ok); }
    .event.failed::before { background: var(--danger); }
    .event.active { background: #f7fbfb; }
    pre {
      margin: 0;
      padding: 12px;
      background: #111827;
      color: #e5e7eb;
      border-radius: 6px;
      overflow: auto;
      font-size: 12px;
      line-height: 1.55;
      min-height: 160px;
    }
    .empty {
      color: var(--muted);
      padding: 18px;
      font-size: 13px;
    }
    .stats {
      display: grid;
      grid-template-columns: repeat(3, 1fr);
      gap: 8px;
      padding: 12px;
      border-bottom: 1px solid var(--line);
      background: #fbfcfd;
    }
    .stat { border: 1px solid var(--line); border-radius: 6px; padding: 9px; background: #fff; }
    .stat b { display: block; font-size: 18px; }
    .stat span { color: var(--muted); font-size: 11px; }
    .detail-body { display: grid; gap: 12px; padding: 12px; align-content: start; }
    .kv { display: grid; grid-template-columns: 92px 1fr; gap: 8px; font-size: 13px; }
    .kv span:first-child { color: var(--muted); }
    .view-tabs { display: flex; gap: 6px; }
    .view-tabs button.active { background: #e7f6f3; border-color: #b8ded7; color: var(--accent); font-weight: 700; }
    .graph-wrap { overflow: auto; min-height: 0; padding: 12px; background: #fbfcfd; flex: 1; }
    .graph-svg { min-width: 100%; min-height: 420px; }
    .graph-node { cursor: pointer; }
    .graph-node rect { fill: #fff; stroke: #94a3b8; stroke-width: 1.5; }
    .graph-node.active rect { stroke: var(--accent); stroke-width: 2.5; fill: #e7f6f3; }
    .graph-node.done rect { fill: #ecfdf5; stroke: #34d399; }
    .graph-node.current rect { fill: #fff5e5; stroke: var(--accent-2); stroke-width: 2.5; }
    .graph-node.subgraph-active rect { stroke: #2563eb; stroke-width: 2.5; fill: #eff6ff; }
    .graph-node.not-resumable rect { stroke-dasharray: 4 3; }
    .graph-node text { font-size: 11px; fill: #1e2732; pointer-events: none; }
    .graph-edge { stroke: #94a3b8; stroke-width: 1.5; fill: none; marker-end: url(#arrow); }
    .graph-sub { margin-top: 16px; border-top: 1px dashed var(--line); padding-top: 12px; }
    .graph-sub-title { font-size: 12px; font-weight: 700; color: var(--muted); margin-bottom: 8px; }
    .time-travel { display: flex; gap: 8px; padding: 0 12px 12px; flex-wrap: wrap; align-items: center; }
    .checkpoint-list { display: grid; gap: 8px; }
    .checkpoint-item {
      border: 1px solid var(--line);
      border-radius: 6px;
      padding: 10px;
      cursor: pointer;
      background: #fff;
    }
    .checkpoint-item:hover, .checkpoint-item.active { background: #f0f7f6; border-color: #b8ded7; }
    .checkpoint-item .title { font-size: 13px; font-weight: 700; }
    @media (max-width: 1080px) {
      main { grid-template-columns: 320px 1fr; grid-template-rows: minmax(0, 1fr) minmax(260px, 40vh); }
      .detail { grid-column: 1 / -1; }
    }
    @media (max-width: 760px) {
      header { height: auto; min-height: 58px; align-items: flex-start; flex-direction: column; padding: 10px 12px; gap: 8px; }
      main { height: auto; min-height: calc(100vh - 78px); grid-template-columns: 1fr; grid-template-rows: 320px 460px 360px; }
      .toolbar { flex-wrap: wrap; }
    }
  </style>
</head>
<body>
  <header>
    <h1>AgentFlow Observability</h1>
    <div class="toolbar">
      <select id="statusFilter" aria-label="Status filter">
        <option value="">All runs</option>
        <option value="running">Running</option>
        <option value="paused">Paused</option>
        <option value="completed">Completed</option>
        <option value="failed">Failed</option>
      </select>
      <button id="refreshButton">Refresh</button>
      <button class="primary" id="liveButton">Live on</button>
    </div>
  </header>
  <main>
    <section class="panel">
      <div class="panel-head"><div class="panel-title">Sessions</div><span class="badge" id="runCount">0</span></div>
      <div class="list" id="runs"><div class="empty">No sessions</div></div>
    </section>
    <section class="panel">
      <div class="panel-head">
        <div class="panel-title">Run View</div>
        <div class="view-tabs">
          <button id="timelineTab" class="active">Timeline</button>
          <button id="graphTab">Graph</button>
        </div>
      </div>
      <div class="panel-head" style="border-top:0; min-height:40px;">
        <span class="badge" id="selectedRun">No run</span>
      </div>
      <div class="stats">
        <div class="stat"><b id="statEvents">0</b><span>events</span></div>
        <div class="stat"><b id="statTools">0</b><span>tool calls</span></div>
        <div class="stat"><b id="statLLM">0</b><span>LLM calls</span></div>
      </div>
      <div class="timeline" id="events"><div class="empty">Select a session</div></div>
      <div class="graph-wrap" id="graphView" hidden><div class="empty">Graph export requires Framework wiring</div></div>
      <div class="time-travel" id="timeTravelBar" hidden>
        <button class="primary" id="resumeStepButton" disabled>Resume from selected node</button>
        <button id="resumeCheckpointButton" disabled>Resume from checkpoint</button>
        <span class="meta" id="selectedNodeLabel">No node selected</span>
      </div>
    </section>
    <section class="panel detail">
      <div class="panel-head"><div class="panel-title">Details</div><span class="badge" id="detailType">Empty</span></div>
      <div class="detail-body" id="details"><div class="empty">Select an event</div></div>
    </section>
  </main>
  <script>
    const state = { runs: [], events: [], selectedRun: '', selectedEvent: null, stream: null, live: true, view: 'timeline', graph: null, steps: null, checkpoints: null, selectedNode: '', selectedCheckpoint: null, graphEnabled: false, resumeEnabled: false, checkpointEnabled: false, activeSubgraphs: {}, nodeMeta: {} };
    const $ = (id) => document.getElementById(id);
    const fmtTime = (value) => value ? new Date(value).toLocaleTimeString() : '-';
    const escapeHTML = (value) => String(value ?? '').replace(/[&<>"']/g, (char) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char]));
    const eventKind = (type) => {
      if (type.includes('Tool')) return 'tool';
      if (type.includes('LLM')) return 'llm';
      if (type.includes('Failed') || type.includes('Denied')) return 'failed';
      if (type.includes('Run') || type.includes('Step')) return 'run';
      return '';
    };
    async function loadGraph() {
      try {
        const res = await fetch('api/graph');
        if (!res.ok) return;
        state.graph = await res.json();
        state.graphEnabled = true;
        state.nodeMeta = buildNodeMeta(state.graph);
        $('timeTravelBar').hidden = false;
      } catch (_) {}
    }
    async function loadCheckpoints(runID) {
      state.checkpoints = null;
      state.selectedCheckpoint = null;
      try {
        const res = await fetch('api/runs/' + encodeURIComponent(runID) + '/checkpoints');
        if (!res.ok) return;
        state.checkpoints = await res.json();
        state.checkpointEnabled = true;
      } catch (_) {}
      updateTimeTravelBar();
      renderDetails();
    }
    async function loadSteps(runID) {
      state.steps = null;
      state.selectedNode = '';
      try {
        const res = await fetch('api/runs/' + encodeURIComponent(runID) + '/steps');
        if (!res.ok) return;
        state.steps = await res.json();
        state.resumeEnabled = true;
      } catch (_) {}
      renderGraph();
      updateTimeTravelBar();
    }
    function setView(view) {
      state.view = view;
      $('timelineTab').classList.toggle('active', view === 'timeline');
      $('graphTab').classList.toggle('active', view === 'graph');
      $('events').hidden = view !== 'timeline';
      $('graphView').hidden = view !== 'graph';
      if (view === 'graph') renderGraph();
    }
    function buildNodeMeta(graph) {
      const meta = {};
      const ingest = (view) => {
        (view && view.nodes || []).forEach((node) => { meta[node.id] = node; });
      };
      if (graph) {
        ingest(graph.workflow);
        Object.values(graph.workflows || {}).forEach(ingest);
      }
      return meta;
    }
    function syncSubgraphOverlay() {
      state.activeSubgraphs = {};
      state.events.forEach((record) => {
        const payload = record.event.payload || {};
        let body = payload;
        if (typeof payload === 'string') {
          try { body = JSON.parse(payload); } catch (_) { body = {}; }
        }
        if (record.event.type === 'SubgraphStarted' && body.node_id) {
          state.activeSubgraphs[body.node_id] = body.subgraph_ref || true;
        }
        if (record.event.type === 'SubgraphCompleted' && body.node_id) {
          delete state.activeSubgraphs[body.node_id];
        }
      });
    }
    function layoutGraph(view) {
      const nodes = view.nodes || [];
      const edges = view.edges || [];
      const levels = {};
      nodes.forEach((node) => { levels[node.id] = 0; });
      for (let i = 0; i < nodes.length; i++) {
        edges.forEach((edge) => {
          if (levels[edge.to] <= levels[edge.from]) levels[edge.to] = levels[edge.from] + 1;
        });
      }
      const grouped = {};
      nodes.forEach((node) => {
        const level = levels[node.id] || 0;
        if (!grouped[level]) grouped[level] = [];
        grouped[level].push(node);
      });
      const positions = {};
      Object.keys(grouped).sort((a,b)=>a-b).forEach((level) => {
        grouped[level].forEach((node, index) => {
          positions[node.id] = { x: 40 + Number(level) * 180, y: 40 + index * 90 };
        });
      });
      return { nodes, edges, positions };
    }
    function stepNodeIDs() {
      if (!state.steps || !state.steps.steps) return new Set();
      return new Set(state.steps.steps.map((step) => step.node_id));
    }
    function renderGraphView(container, view, title) {
      if (!view || !view.nodes || !view.nodes.length) {
        container.innerHTML = '<div class="empty">No workflow graph</div>';
        return;
      }
      syncSubgraphOverlay();
      const { nodes, edges, positions } = layoutGraph(view);
      const done = stepNodeIDs();
      const current = state.steps && state.steps.current_node_id ? state.steps.current_node_id : '';
      const width = Math.max(640, ...Object.values(positions).map((p) => p.x + 160));
      const height = Math.max(320, ...Object.values(positions).map((p) => p.y + 70));
      let html = title ? '<div class="graph-sub-title">' + escapeHTML(title) + '</div>' : '';
      html += '<svg class="graph-svg" viewBox="0 0 ' + width + ' ' + height + '" width="' + width + '" height="' + height + '">';
      html += '<defs><marker id="arrow" markerWidth="8" markerHeight="8" refX="7" refY="3" orient="auto"><path d="M0,0 L7,3 L0,6 Z" fill="#94a3b8"/></marker></defs>';
      edges.forEach((edge) => {
        const from = positions[edge.from];
        const to = positions[edge.to];
        if (!from || !to) return;
        html += '<path class="graph-edge" d="M' + (from.x + 120) + ',' + (from.y + 24) + ' C' + (from.x + 150) + ',' + from.y + ' ' + (to.x - 20) + ',' + to.y + ' ' + to.x + ',' + (to.y + 24) + '"/>';
      });
      nodes.forEach((node) => {
        const pos = positions[node.id];
        const classes = ['graph-node'];
        if (state.selectedNode === node.id) classes.push('active');
        if (done.has(node.id)) classes.push('done');
        if (current === node.id) classes.push('current');
        if (state.activeSubgraphs[node.id]) classes.push('subgraph-active');
        if (node.resumable === false) classes.push('not-resumable');
        html += '<g class="' + classes.join(' ') + '" data-node="' + escapeHTML(node.id) + '" transform="translate(' + pos.x + ',' + pos.y + ')">';
        html += '<rect width="120" height="48" rx="8"></rect>';
        html += '<text x="8" y="18" font-weight="700">' + escapeHTML(node.id) + '</text>';
        html += '<text x="8" y="34" fill="#697586">' + escapeHTML(node.kind + (node.ref ? ':' + node.ref : '')) + '</text>';
        html += '</g>';
      });
      html += '</svg>';
      container.innerHTML = html;
      container.querySelectorAll('.graph-node').forEach((node) => node.onclick = () => {
        state.selectedNode = node.dataset.node;
        renderGraph();
        updateTimeTravelBar();
      });
    }
    function renderGraph() {
      const container = $('graphView');
      if (!state.graphEnabled) {
        container.innerHTML = '<div class="empty">Graph export requires Framework wiring on ObservabilityHTTPHandlerConfig.Framework</div>';
        return;
      }
      let html = '';
      const root = document.createElement('div');
      renderGraphView(root, state.graph.workflow, state.graph.name + ' (' + (state.graph.mode || 'mode') + ')');
      html += root.innerHTML;
      if (state.graph.workflows) {
        Object.entries(state.graph.workflows).forEach(([name, view]) => {
          const sub = document.createElement('div');
          sub.className = 'graph-sub';
          renderGraphView(sub, view, 'subgraph: ' + name);
          html += sub.outerHTML;
        });
      }
      container.innerHTML = html;
      container.querySelectorAll('.graph-node').forEach((node) => node.onclick = () => {
        state.selectedNode = node.dataset.node;
        renderGraph();
        updateTimeTravelBar();
      });
    }
    function updateTimeTravelBar() {
      const meta = state.nodeMeta[state.selectedNode] || {};
      const hint = meta.resume_hint || (meta.resumable === false ? 'This node cannot be resumed from step' : '');
      $('selectedNodeLabel').textContent = state.selectedNode
        ? ('Node: ' + state.selectedNode + (hint ? ' — ' + hint : ''))
        : 'No node selected';
      $('resumeStepButton').disabled = !(state.resumeEnabled && state.selectedNode && state.selectedRun && meta.resumable !== false);
      $('resumeCheckpointButton').disabled = !(state.checkpointEnabled && state.selectedCheckpoint && state.selectedRun);
    }
    async function resumeFromCheckpoint() {
      if (!state.selectedRun || !state.selectedCheckpoint) return;
      $('resumeCheckpointButton').disabled = true;
      const res = await fetch('api/runs/' + encodeURIComponent(state.selectedRun) + '/resume-from-checkpoint', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ version: state.selectedCheckpoint.version }),
      });
      const body = await res.json();
      if (!res.ok) {
        alert(body.error || 'resume failed');
        updateTimeTravelBar();
        return;
      }
      await loadSteps(state.selectedRun);
      await loadCheckpoints(state.selectedRun);
      await loadRuns();
      setView('timeline');
      await selectRun(state.selectedRun);
    }
    async function resumeFromStep() {
      if (!state.selectedRun || !state.selectedNode) return;
      $('resumeStepButton').disabled = true;
      const res = await fetch('api/runs/' + encodeURIComponent(state.selectedRun) + '/resume-from-step', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ node_id: state.selectedNode }),
      });
      const body = await res.json();
      if (!res.ok) {
        alert(body.error || 'resume failed');
        updateTimeTravelBar();
        return;
      }
      await loadSteps(state.selectedRun);
      await loadRuns();
      setView('timeline');
      await selectRun(state.selectedRun);
    }
    async function loadRuns() {
      const status = $('statusFilter').value;
      const res = await fetch('api/runs?limit=100' + (status ? '&status=' + encodeURIComponent(status) : ''));
      const body = await res.json();
      state.runs = body.runs || [];
      renderRuns();
      if (!state.selectedRun && state.runs[0]) selectRun(state.runs[0].run_id);
    }
    async function selectRun(runID) {
      state.selectedRun = runID;
      state.selectedEvent = null;
      closeStream();
      const res = await fetch('api/runs/' + encodeURIComponent(runID) + '/events?limit=500');
      const body = await res.json();
      state.events = body.events || [];
      await loadSteps(runID);
      await loadCheckpoints(runID);
      renderRuns();
      renderEvents();
      renderDetails();
      if (state.view === 'graph') renderGraph();
      if (state.live) openStream();
    }
    function openStream() {
      if (!state.selectedRun || state.stream) return;
      const last = state.events.length ? state.events[state.events.length - 1].sequence : 0;
      state.stream = new EventSource('api/runs/' + encodeURIComponent(state.selectedRun) + '/stream?after_sequence=' + last);
      state.stream.addEventListener('runtime_event', (message) => {
        const record = JSON.parse(message.data);
        if (state.events.some((item) => item.id === record.id)) return;
        state.events.push(record);
        renderEvents();
        if (record.event.type.includes('Step') || record.event.type.includes('Subgraph')) {
          loadSteps(state.selectedRun);
        }
        if (record.event.type.includes('Subgraph')) renderGraph();
      });
      state.stream.onerror = () => { closeStream(); setTimeout(() => state.live && openStream(), 2000); };
    }
    function closeStream() {
      if (state.stream) state.stream.close();
      state.stream = null;
    }
    function renderRuns() {
      $('runCount').textContent = state.runs.length;
      $('runs').innerHTML = state.runs.length ? state.runs.map((run) =>
        '<div class="run ' + (run.run_id === state.selectedRun ? 'active' : '') + '" data-run="' + escapeHTML(run.run_id) + '">' +
          '<div class="run-main"><div class="run-id">' + escapeHTML(run.run_id) + '</div><span class="badge ' + escapeHTML(run.status) + '">' + escapeHTML(run.status) + '</span></div>' +
          '<div class="meta"><span>' + escapeHTML(run.scenario_name || 'scenario') + '</span><span>' + run.event_count + ' events</span><span>' + fmtTime(run.last_seen_at) + '</span></div>' +
        '</div>').join('') : '<div class="empty">No sessions</div>';
      document.querySelectorAll('.run').forEach((node) => node.onclick = () => selectRun(node.dataset.run));
    }
    function renderEvents() {
      $('selectedRun').textContent = state.selectedRun || 'No run';
      $('statEvents').textContent = state.events.length;
      $('statTools').textContent = state.events.filter((record) => record.event.type.includes('ToolCalled')).length;
      $('statLLM').textContent = state.events.filter((record) => record.event.type.includes('LLMCalled')).length;
      $('events').innerHTML = state.events.length ? state.events.map((record) =>
        '<div class="event ' + eventKind(record.event.type) + ' ' + (state.selectedEvent && state.selectedEvent.id === record.id ? 'active' : '') + '" data-id="' + record.id + '">' +
          '<div class="event-main"><div class="event-type">' + escapeHTML(record.event.type) + '</div><span class="badge">#' + record.sequence + '</span></div>' +
          '<div class="meta"><span>' + fmtTime(record.event.timestamp) + '</span><span>' + escapeHTML(record.event.trace_id || 'trace -') + '</span><span>' + escapeHTML(record.event.span_id || 'span -') + '</span></div>' +
        '</div>').join('') : '<div class="empty">No events</div>';
      document.querySelectorAll('.event').forEach((node) => node.onclick = () => {
        state.selectedEvent = state.events.find((record) => String(record.id) === node.dataset.id);
        state.selectedCheckpoint = null;
        renderEvents();
        renderDetails();
        updateTimeTravelBar();
      });
    }
    function renderDetails() {
      const record = state.selectedEvent;
      $('detailType').textContent = record ? record.event.type : (state.selectedCheckpoint ? 'Checkpoint' : 'Run');
      if (record) {
        $('details').innerHTML =
          '<div class="kv"><span>Run</span><span>' + escapeHTML(record.event.run_id) + '</span></div>' +
          '<div class="kv"><span>Scenario</span><span>' + escapeHTML(record.event.scenario_name || '-') + '</span></div>' +
          '<div class="kv"><span>Sequence</span><span>' + record.sequence + '</span></div>' +
          '<div class="kv"><span>Occurred</span><span>' + escapeHTML(record.event.timestamp) + '</span></div>' +
          '<div class="kv"><span>Stored</span><span>' + escapeHTML(record.created_at) + '</span></div>' +
          '<pre>' + escapeHTML(JSON.stringify(record.event.payload || {}, null, 2)) + '</pre>';
        return;
      }
      let html = '';
      if (state.selectedRun) {
        html += '<div class="kv"><span>Run</span><span>' + escapeHTML(state.selectedRun) + '</span></div>';
      }
      if (state.selectedCheckpoint) {
        const cp = state.selectedCheckpoint;
        html += '<div class="kv"><span>Version</span><span>v' + cp.version + '</span></div>';
        html += '<div class="kv"><span>Status</span><span>' + escapeHTML(cp.status) + '</span></div>';
        html += '<div class="kv"><span>Steps</span><span>' + cp.step_count + '</span></div>';
        html += '<div class="kv"><span>Recorded</span><span>' + fmtTime(cp.recorded_at) + '</span></div>';
        if (cp.current_node_id) {
          html += '<div class="kv"><span>Current</span><span>' + escapeHTML(cp.current_node_id) + '</span></div>';
        }
      }
      if (state.checkpointEnabled && state.checkpoints && state.checkpoints.checkpoints) {
        const items = state.checkpoints.checkpoints;
        html += '<div class="panel-title" style="margin-top:8px;">Checkpoint history</div>';
        html += items.length ? '<div class="checkpoint-list">' + items.map((cp) =>
          '<div class="checkpoint-item ' + (state.selectedCheckpoint && state.selectedCheckpoint.version === cp.version ? 'active' : '') + '" data-version="' + cp.version + '">' +
            '<div class="title">v' + cp.version + ' · ' + escapeHTML(cp.status) + '</div>' +
            '<div class="meta"><span>' + cp.step_count + ' steps</span><span>' + fmtTime(cp.recorded_at) + '</span></div>' +
          '</div>').join('') + '</div>' : '<div class="empty">No checkpoints recorded</div>';
      } else if (state.selectedRun) {
        html += '<div class="empty">Checkpoint history requires WithCheckpointHistory wiring</div>';
      }
      if (!html) {
        html = '<div class="empty">Select an event or checkpoint</div>';
      }
      $('details').innerHTML = html;
      document.querySelectorAll('.checkpoint-item').forEach((node) => node.onclick = async () => {
        const version = Number(node.dataset.version);
        state.selectedEvent = null;
        state.selectedCheckpoint = (state.checkpoints.checkpoints || []).find((cp) => cp.version === version) || null;
        renderEvents();
        renderDetails();
        updateTimeTravelBar();
      });
    }
    $('refreshButton').onclick = () => loadRuns();
    $('statusFilter').onchange = () => loadRuns();
    $('timelineTab').onclick = () => setView('timeline');
    $('graphTab').onclick = () => setView('graph');
    $('resumeStepButton').onclick = () => resumeFromStep();
    $('resumeCheckpointButton').onclick = () => resumeFromCheckpoint();
    $('liveButton').onclick = () => {
      state.live = !state.live;
      $('liveButton').textContent = state.live ? 'Live on' : 'Live off';
      if (state.live) openStream(); else closeStream();
    };
    loadGraph().then(() => loadRuns());
    setInterval(() => { if (state.live) loadRuns(); }, 3000);
  </script>
</body>
</html>`

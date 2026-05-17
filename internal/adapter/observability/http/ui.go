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
      <div class="panel-head"><div class="panel-title">Timeline</div><span class="badge" id="selectedRun">No run</span></div>
      <div class="stats">
        <div class="stat"><b id="statEvents">0</b><span>events</span></div>
        <div class="stat"><b id="statTools">0</b><span>tool calls</span></div>
        <div class="stat"><b id="statLLM">0</b><span>LLM calls</span></div>
      </div>
      <div class="timeline" id="events"><div class="empty">Select a session</div></div>
    </section>
    <section class="panel detail">
      <div class="panel-head"><div class="panel-title">Details</div><span class="badge" id="detailType">Empty</span></div>
      <div class="detail-body" id="details"><div class="empty">Select an event</div></div>
    </section>
  </main>
  <script>
    const state = { runs: [], events: [], selectedRun: '', selectedEvent: null, stream: null, live: true };
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
      renderRuns();
      renderEvents();
      renderDetails();
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
        renderEvents();
        renderDetails();
      });
    }
    function renderDetails() {
      const record = state.selectedEvent;
      $('detailType').textContent = record ? record.event.type : 'Empty';
      if (!record) {
        $('details').innerHTML = '<div class="empty">Select an event</div>';
        return;
      }
      $('details').innerHTML =
        '<div class="kv"><span>Run</span><span>' + escapeHTML(record.event.run_id) + '</span></div>' +
        '<div class="kv"><span>Scenario</span><span>' + escapeHTML(record.event.scenario_name || '-') + '</span></div>' +
        '<div class="kv"><span>Sequence</span><span>' + record.sequence + '</span></div>' +
        '<div class="kv"><span>Occurred</span><span>' + escapeHTML(record.event.timestamp) + '</span></div>' +
        '<div class="kv"><span>Stored</span><span>' + escapeHTML(record.created_at) + '</span></div>' +
        '<pre>' + escapeHTML(JSON.stringify(record.event.payload || {}, null, 2)) + '</pre>';
    }
    $('refreshButton').onclick = () => loadRuns();
    $('statusFilter').onchange = () => loadRuns();
    $('liveButton').onclick = () => {
      state.live = !state.live;
      $('liveButton').textContent = state.live ? 'Live on' : 'Live off';
      if (state.live) openStream(); else closeStream();
    };
    loadRuns();
    setInterval(() => { if (state.live) loadRuns(); }, 3000);
  </script>
</body>
</html>`

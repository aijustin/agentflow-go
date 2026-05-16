package debugui

const indexHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>agentflow-go debug console</title>
  <style>
    :root {
      --ink: #15120d;
      --muted: #6f675c;
      --paper: #f3ead8;
      --panel: rgba(255, 250, 239, .78);
      --line: rgba(41, 32, 20, .16);
      --hot: #ff4e1f;
      --acid: #b7ff2a;
      --blue: #315cff;
      --shadow: 0 24px 80px rgba(54, 40, 18, .18);
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      min-height: 100vh;
      color: var(--ink);
      font-family: ui-serif, Georgia, "Times New Roman", serif;
      background:
        radial-gradient(circle at 12% 8%, rgba(255,78,31,.28), transparent 32%),
        radial-gradient(circle at 92% 18%, rgba(183,255,42,.35), transparent 28%),
        linear-gradient(135deg, #f7ecd4 0%, #e7dcc7 48%, #f6efe3 100%);
    }
    body::before {
      content: "";
      position: fixed;
      inset: 0;
      pointer-events: none;
      opacity: .22;
      background-image: linear-gradient(rgba(0,0,0,.08) 1px, transparent 1px), linear-gradient(90deg, rgba(0,0,0,.08) 1px, transparent 1px);
      background-size: 34px 34px;
      mask-image: radial-gradient(circle at 50% 20%, black, transparent 72%);
    }
    header {
      padding: 34px clamp(20px, 5vw, 72px) 20px;
      display: grid;
      grid-template-columns: 1fr auto;
      gap: 24px;
      align-items: end;
    }
    h1 {
      margin: 0;
      font-size: clamp(42px, 7vw, 92px);
      line-height: .86;
      letter-spacing: -0.07em;
      max-width: 980px;
    }
    .stamp {
      border: 2px solid var(--ink);
      transform: rotate(4deg);
      padding: 12px 18px;
      font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
      text-transform: uppercase;
      background: var(--acid);
      box-shadow: 6px 6px 0 var(--ink);
    }
    main {
      padding: 12px clamp(20px, 5vw, 72px) 64px;
      display: grid;
      grid-template-columns: minmax(320px, 420px) minmax(0, 1fr);
      gap: 22px;
    }
    .panel {
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 28px;
      box-shadow: var(--shadow);
      backdrop-filter: blur(18px);
      overflow: hidden;
    }
    .panel h2 {
      margin: 0;
      padding: 20px 22px;
      border-bottom: 1px solid var(--line);
      font-size: 20px;
      letter-spacing: -.02em;
    }
    .scenario-list { padding: 14px; display: grid; gap: 10px; }
    .scenario {
      border: 1px solid var(--line);
      border-radius: 18px;
      background: rgba(255,255,255,.42);
      padding: 14px;
      cursor: pointer;
      transition: transform .16s ease, background .16s ease, border-color .16s ease;
    }
    .scenario:hover, .scenario.active {
      transform: translateY(-2px);
      border-color: rgba(255,78,31,.58);
      background: rgba(255,255,255,.72);
    }
    .scenario b { display: block; font-size: 16px; margin-bottom: 4px; }
    .scenario span { color: var(--muted); font-size: 13px; line-height: 1.35; }
    .workspace {
      display: grid;
      grid-template-rows: auto auto 1fr;
      min-height: 760px;
    }
    .controls {
      padding: 18px 22px;
      display: grid;
      grid-template-columns: 1fr auto;
      gap: 12px;
      border-bottom: 1px solid var(--line);
    }
    label { display: block; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 11px; text-transform: uppercase; color: var(--muted); margin-bottom: 6px; }
    input, textarea, select {
      width: 100%;
      border: 1px solid var(--line);
      border-radius: 14px;
      padding: 12px 13px;
      background: rgba(255,255,255,.72);
      color: var(--ink);
      font: 14px/1.45 ui-monospace, SFMono-Regular, Menlo, monospace;
      outline: none;
    }
    textarea { min-height: 250px; resize: vertical; }
    .grid2 { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; }
    .real { display: none; grid-column: 1 / -1; gap: 10px; grid-template-columns: repeat(3, minmax(0, 1fr)); }
    .real.show { display: grid; }
    button {
      border: 0;
      border-radius: 999px;
      padding: 12px 18px;
      background: var(--ink);
      color: #fff8e8;
      font-weight: 800;
      cursor: pointer;
      box-shadow: 0 10px 28px rgba(21,18,13,.22);
      transition: transform .16s ease, box-shadow .16s ease;
    }
    button:hover { transform: translateY(-2px); box-shadow: 0 14px 36px rgba(21,18,13,.28); }
    button.secondary { background: var(--blue); }
    button.hot { background: var(--hot); }
    .editor { padding: 18px 22px; display: grid; gap: 12px; border-bottom: 1px solid var(--line); }
    .context-box { min-height: 88px; }
    .output {
      display: grid;
      grid-template-columns: minmax(0, .9fr) minmax(0, 1.1fr);
      min-height: 260px;
    }
    .output > section { padding: 18px 22px; border-right: 1px solid var(--line); overflow: auto; }
    .output > section:last-child { border-right: 0; }
    pre {
      margin: 0;
      white-space: pre-wrap;
      word-break: break-word;
      font: 12px/1.5 ui-monospace, SFMono-Regular, Menlo, monospace;
      color: #282018;
    }
    .timeline { display: grid; gap: 10px; }
    .event {
      border-left: 5px solid var(--hot);
      background: rgba(255,255,255,.56);
      border-radius: 14px;
      padding: 10px 12px;
      font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
      font-size: 12px;
    }
    .event strong { display: block; font-size: 13px; margin-bottom: 4px; }
    .hitl {
      display: none;
      margin-top: 12px;
      padding: 14px;
      border-radius: 18px;
      background: rgba(255,78,31,.12);
      border: 1px solid rgba(255,78,31,.34);
    }
    .hitl.show { display: block; }
    .row { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; }
    @media (max-width: 980px) {
      main { grid-template-columns: 1fr; }
      header { grid-template-columns: 1fr; }
      .output { grid-template-columns: 1fr; }
      .output > section { border-right: 0; border-bottom: 1px solid var(--line); }
      .real { grid-template-columns: 1fr; }
    }
  </style>
</head>
<body>
  <header>
    <h1>Agent Debug Console</h1>
    <div class="stamp">live trace lab</div>
  </header>
  <main>
    <aside class="panel">
      <h2>Scenarios</h2>
      <div id="scenarioList" class="scenario-list"></div>
    </aside>
    <div class="panel workspace">
      <div class="controls">
        <div>
          <label>Prompt</label>
          <input id="prompt" placeholder="Enter prompt..." />
        </div>
        <div class="row" style="align-self:end">
          <button id="runBtn" class="hot">Run scenario</button>
        </div>
        <div id="realFields" class="real">
          <div>
            <label>OpenAI-compatible base URL</label>
            <input id="baseURL" value="http://192.168.31.117:1234/v1" />
          </div>
          <div>
            <label>Model ID</label>
            <input id="model" value="qwen/qwen3.6-35b-a3b" />
          </div>
          <div>
            <label>API Key</label>
            <input id="apiKey" type="password" placeholder="sk-..." />
          </div>
        </div>
      </div>
      <div class="editor">
        <label>Scenario YAML</label>
        <textarea id="yaml"></textarea>
        <label>Runtime context JSON</label>
        <textarea id="context" class="context-box" placeholder='{"key":"value"}'></textarea>
      </div>
      <div class="output">
        <section>
          <h2 style="padding:0 0 12px;border:0">Run result</h2>
          <pre id="result">No run yet.</pre>
          <div id="hitl" class="hitl">
            <label>Human decision token</label>
            <input id="token" />
            <div class="row" style="margin-top:10px">
              <select id="decision">
                <option value="approve">approve</option>
                <option value="reject">reject</option>
                <option value="amend">amend</option>
              </select>
              <button id="resumeBtn" class="secondary">Resume</button>
            </div>
            <label style="margin-top:10px">Amendment JSON</label>
            <input id="amendment" placeholder='{"note":"continue"}' />
          </div>
        </section>
        <section>
          <h2 style="padding:0 0 12px;border:0">Execution timeline</h2>
          <div id="events" class="timeline"></div>
        </section>
      </div>
    </div>
  </main>
  <script>
    const state = { scenarios: [], selected: null, currentRun: null };
    const $ = id => document.getElementById(id);
    const pretty = v => JSON.stringify(v, null, 2);

    async function loadScenarios() {
      const res = await fetch('/api/scenarios');
      state.scenarios = await res.json();
      renderScenarios();
      selectScenario(state.scenarios[0].id);
    }

    function renderScenarios() {
      $('scenarioList').innerHTML = state.scenarios.map(s =>
        '<div class="scenario" data-id="' + escapeHTML(s.id) + '">' +
          '<b>' + escapeHTML(s.name) + '</b>' +
          '<span>' + escapeHTML(s.description) + '</span>' +
        '</div>'
      ).join('');
      document.querySelectorAll('.scenario').forEach(el => el.onclick = () => selectScenario(el.dataset.id));
    }

    function selectScenario(id) {
      state.selected = state.scenarios.find(s => s.id === id);
      document.querySelectorAll('.scenario').forEach(el => el.classList.toggle('active', el.dataset.id === id));
      $('yaml').value = state.selected.yaml;
      $('prompt').value = state.selected.prompt || '';
      $('context').value = state.selected.context || '';
      $('realFields').classList.toggle('show', !!state.selected.real_model);
      $('result').textContent = 'Ready: ' + state.selected.name;
      $('events').innerHTML = '';
      $('hitl').classList.remove('show');
    }

    async function runScenario() {
      $('runBtn').disabled = true;
      $('result').textContent = 'Running...';
      $('events').innerHTML = '';
      try {
        const rawContext = $('context').value.trim();
        const body = {
          scenario_id: state.selected.id,
          yaml: $('yaml').value,
          prompt: $('prompt').value,
          context: rawContext ? JSON.parse(rawContext) : undefined,
          real_model: {
            enabled: !!state.selected.real_model,
            base_url: $('baseURL').value,
            model: $('model').value,
            api_key: $('apiKey').value
          }
        };
        const res = await fetch('/api/run', { method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body) });
        if (!res.ok) throw new Error(await res.text());
        const data = await res.json();
        state.currentRun = data.run_id;
        renderRun(data);
      } catch (err) {
        $('result').textContent = err.message;
      } finally {
        $('runBtn').disabled = false;
      }
    }

    async function resumeRun() {
      const body = {
        token: $('token').value,
        decision: $('decision').value,
        amendment: $('amendment').value ? JSON.parse($('amendment').value) : undefined
      };
      const res = await fetch('/api/resume', { method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body) });
      if (!res.ok) {
        $('result').textContent = await res.text();
        return;
      }
      renderRun(await res.json());
    }

    function renderRun(data) {
      $('result').textContent = pretty(data);
      $('hitl').classList.toggle('show', !!data.token);
      if (data.token) $('token').value = data.token;
      $('events').innerHTML = (data.events || []).map(e =>
        '<div class="event">' +
          '<strong>' + escapeHTML(e.type) + '</strong>' +
          '<div>' + escapeHTML(new Date(e.timestamp).toLocaleTimeString()) + ' · ' + escapeHTML(e.run_id) + '</div>' +
          '<pre>' + escapeHTML(formatPayload(e.payload)) + '</pre>' +
        '</div>'
      ).join('') || '<div class="event"><strong>No events captured yet</strong></div>';
    }

    function formatPayload(v) {
      if (!v) return '';
      if (typeof v === 'string') {
        try { return pretty(JSON.parse(v)); } catch { return v; }
      }
      return pretty(v);
    }

    function escapeHTML(value) {
      return String(value || '').replace(/[&<>"']/g, ch => ({
        '&': '&amp;',
        '<': '&lt;',
        '>': '&gt;',
        '"': '&quot;',
        "'": '&#39;'
      }[ch]));
    }

    $('runBtn').onclick = runScenario;
    $('resumeBtn').onclick = resumeRun;
    loadScenarios();
  </script>
</body>
</html>`

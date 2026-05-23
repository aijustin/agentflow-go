package http

const indexHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>AgentFlow 可观测性</title>
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
    .hitl-bar {
      display: flex;
      align-items: center;
      gap: 8px;
      padding: 8px 16px;
      border-top: 1px solid var(--line);
      background: #fffbeb;
    }
    .hitl-bar .meta { flex: 1; }
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
    .editor-toolbar, .compare-toolbar, .thread-toolbar { display: flex; gap: 8px; padding: 0 12px 12px; flex-wrap: wrap; align-items: center; }
    .editor-toolbar select { min-width: 120px; }
    .graph-node.editing rect { stroke: #2563eb; }
    .graph-node.multi-selected rect { stroke: #7c3aed; stroke-width: 2.5; fill: #f5f3ff; }
    .graph-port { fill: #2563eb; cursor: crosshair; }
    .graph-node.diff-a rect { stroke: #b45309; fill: #fff5e5; }
    .graph-node.diff-b rect { stroke: #2563eb; fill: #eff6ff; }
    .graph-node.diff-changed rect { stroke: #b42318; fill: #fff0ed; }
    .graph-edge.active { stroke: #2563eb; stroke-width: 2.5; }
    .thread-item { border: 1px solid var(--line); border-radius: 6px; padding: 10px; margin-bottom: 8px; cursor: pointer; }
    .thread-item.active { background: #f0f7f6; border-color: #b8ded7; }
    .editor-props { padding: 0 12px 12px; display: grid; gap: 8px; }
    .editor-props textarea { min-height: 96px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; border: 1px solid var(--line); border-radius: 6px; padding: 8px; }
    .editor-props input { min-height: 34px; border: 1px solid var(--line); border-radius: 6px; padding: 0 8px; font: inherit; }
    .editor-palette { display: flex; gap: 6px; padding: 0 12px 8px; flex-wrap: wrap; align-items: center; }
    .editor-palette .meta { margin-right: 4px; }
    .node-chip { border: 1px solid var(--line); border-radius: 999px; padding: 4px 10px; font-size: 12px; background: #fff; cursor: pointer; }
    .node-chip:hover { background: #f0f7f6; border-color: #b8ded7; }
    .editor-run-bar { display: flex; gap: 8px; padding: 0 12px 12px; flex-wrap: wrap; align-items: center; }
    .editor-run-bar input { flex: 1; min-width: 180px; min-height: 34px; border: 1px solid var(--line); border-radius: 6px; padding: 0 8px; font: inherit; }
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
    <h1 data-i18n="app.title">AgentFlow 可观测性</h1>
    <div class="toolbar">
      <select id="langSelect" aria-label="Language">
        <option value="zh-CN">中文</option>
        <option value="en">English</option>
      </select>
      <select id="statusFilter" data-i18n-aria="filter.aria">
        <option value="" data-i18n="filter.all">全部运行</option>
        <option value="running" data-i18n="filter.running">运行中</option>
        <option value="paused" data-i18n="filter.paused">已暂停</option>
        <option value="completed" data-i18n="filter.completed">已完成</option>
        <option value="failed" data-i18n="filter.failed">失败</option>
      </select>
      <button id="refreshButton" data-i18n="action.refresh">刷新</button>
      <button class="primary" id="liveButton" data-i18n="action.liveOn">实时开</button>
    </div>
  </header>
  <main>
    <section class="panel">
      <div class="panel-head"><div class="panel-title" data-i18n="panel.sessions">会话</div><span class="badge" id="runCount">0</span></div>
      <div class="list" id="runs"><div class="empty" data-i18n="empty.noSessions">暂无会话</div></div>
    </section>
    <section class="panel">
      <div class="panel-head">
        <div class="panel-title" data-i18n="panel.runView">运行视图</div>
        <div class="view-tabs">
          <button id="timelineTab" class="active" data-i18n="tab.timeline">时间线</button>
          <button id="graphTab" data-i18n="tab.graph">图</button>
          <button id="editorTab" data-i18n="tab.editor">编辑器</button>
          <button id="compareTab" data-i18n="tab.compare">对比</button>
          <button id="threadTab" data-i18n="tab.thread">线程</button>
        </div>
      </div>
      <div class="panel-head" style="border-top:0; min-height:40px;">
        <span class="badge" id="selectedRun" data-i18n="empty.noRun">未选择运行</span>
      </div>
      <div class="stats">
        <div class="stat"><b id="statEvents">0</b><span data-i18n="stat.events">事件</span></div>
        <div class="stat"><b id="statTools">0</b><span data-i18n="stat.tools">工具调用</span></div>
        <div class="stat"><b id="statLLM">0</b><span data-i18n="stat.llm">LLM 调用</span></div>
      </div>
      <div class="timeline" id="events"><div class="empty" data-i18n="empty.selectSession">请选择会话</div></div>
      <div class="graph-wrap" id="graphView" hidden><div class="empty" data-i18n="empty.graphRequiresFramework">需要接入 Framework 才能导出图</div></div>
      <div class="graph-wrap" id="editorView" hidden>
        <div class="editor-toolbar">
          <select id="editorTargetSelect" data-i18n-aria="editor.canvasAria"></select>
          <button id="addSubgraphButton" data-i18n="editor.addSubgraph">添加子图</button>
          <select id="editorNodeKind">
            <option value="transform">transform</option>
            <option value="agent">agent</option>
            <option value="tool">tool</option>
            <option value="skill">skill</option>
            <option value="human_gate">human_gate</option>
            <option value="subgraph">subgraph</option>
            <option value="map">map</option>
            <option value="loop">loop</option>
          </select>
          <button id="addNodeButton" data-i18n="editor.addNode">添加节点</button>
          <button id="undoEditorButton" disabled data-i18n="editor.undo">撤销</button>
          <button id="redoEditorButton" disabled data-i18n="editor.redo">重做</button>
          <button id="connectModeButton" data-i18n="editor.connect">连边</button>
          <button id="deleteEdgeButton" data-i18n="editor.deleteEdge">删除边</button>
          <button id="deleteNodeButton" data-i18n="editor.delete">删除</button>
          <button id="validateGraphButton" data-i18n="editor.validate">校验</button>
          <button id="yamlGraphButton" data-i18n="editor.exportYaml">导出 YAML</button>
          <button id="importYamlButton" data-i18n="editor.importYaml">导入 YAML</button>
          <input type="file" id="importYamlFile" accept=".yaml,.yml,text/yaml" hidden />
          <button id="previewSaveButton" data-i18n="editor.previewSave">预览保存</button>
          <button id="saveGraphButton" data-i18n="editor.saveScenario">保存场景</button>
          <button id="revertGraphButton" data-i18n="editor.revertLoaded">恢复到已加载</button>
          <button class="primary" id="codegenGraphButton" data-i18n="editor.exportGo">导出 Go</button>
        </div>
        <div class="editor-palette" id="editorPalette">
          <span class="meta" data-i18n="editor.quickAdd">快速添加：</span>
          <button type="button" class="node-chip" data-kind="transform">transform</button>
          <button type="button" class="node-chip" data-kind="agent">agent</button>
          <button type="button" class="node-chip" data-kind="tool">tool</button>
          <button type="button" class="node-chip" data-kind="skill">skill</button>
          <button type="button" class="node-chip" data-kind="human_gate">human_gate</button>
          <button type="button" class="node-chip" data-kind="subgraph">subgraph</button>
          <button type="button" class="node-chip" data-kind="map">map</button>
          <button type="button" class="node-chip" data-kind="loop">loop</button>
        </div>
        <div class="editor-run-bar">
          <input id="editorRunPrompt" data-i18n-placeholder="editor.runPromptPlaceholder" placeholder="Studio 运行提示词（可选）" />
          <button class="primary" id="runEditorGraphButton" data-i18n="editor.runGraph">运行图</button>
        </div>
        <div class="editor-props" id="editorProps" hidden>
          <div class="meta" data-i18n="editor.nodeProps">所选节点属性</div>
          <input id="editorNodeRef" data-i18n-placeholder="editor.refPlaceholder" placeholder="ref（agent/tool/skill/subgraph）" />
          <input id="editorNodeCondition" data-i18n-placeholder="editor.nodeConditionPlaceholder" placeholder="节点 condition，例如 steps.a.output.ok" />
          <input id="editorNodeDependsOn" data-i18n-placeholder="editor.dependsOnPlaceholder" placeholder="depends_on 逗号分隔，例如 prep,review" />
          <textarea id="editorNodeInput" data-i18n-placeholder="editor.inputPlaceholder" placeholder='input JSON，例如 {"set":{"x":1}}'></textarea>
          <label class="meta"><input type="checkbox" id="editorNodeInterrupt" /> <span data-i18n="editor.interrupt">Declarative interrupt（执行后暂停）</span></label>
          <button id="saveNodePropsButton" data-i18n="editor.applyNodeProps">应用节点属性</button>
        </div>
        <div class="editor-props" id="editorEdgeProps" hidden>
          <div class="meta" data-i18n="editor.edgeProps">所选边</div>
          <input id="editorEdgeFrom" readonly />
          <input id="editorEdgeTo" readonly />
          <input id="editorEdgeCondition" data-i18n-placeholder="editor.edgeConditionPlaceholder" placeholder="边 condition，例如 steps.flag.output.ok" />
          <button id="saveEdgePropsButton" data-i18n="editor.applyEdgeProps">应用边属性</button>
        </div>
        <div id="editorCanvas"><div class="empty" data-i18n="empty.loadGraphToEdit">加载图以开始编辑</div></div>
      </div>
      <div class="graph-wrap" id="compareView" hidden>
        <div class="compare-toolbar">
          <label class="meta" data-i18n="compare.labelB">对比 B</label>
          <select id="compareRunB"></select>
          <button id="compareRunsButton" data-i18n="compare.button">对比</button>
        </div>
        <div id="compareCanvas"><div class="empty" data-i18n="empty.selectCompareRun">请选择运行并选择对比目标</div></div>
      </div>
      <div class="graph-wrap" id="threadView" hidden>
        <div class="thread-toolbar">
          <button id="forkRunButton" data-i18n="thread.fork">分叉当前运行</button>
          <button id="refreshThreadButton" data-i18n="thread.refresh">刷新线程</button>
        </div>
        <div id="threadCanvas"><div class="empty" data-i18n="empty.selectRunForThread">请选择运行以查看分叉线程</div></div>
      </div>
      <div class="hitl-bar" id="hitlBar" hidden>
        <span class="meta" id="hitlBarLabel"></span>
        <button class="primary" id="hitlApproveButton" data-i18n="hitl.approve">批准并继续</button>
        <button id="hitlRejectButton" data-i18n="hitl.reject">拒绝</button>
      </div>
      <div class="time-travel" id="timeTravelBar" hidden>
        <button class="primary" id="resumeStepButton" disabled data-i18n="timeTravel.resumeStep">从所选节点恢复</button>
        <button id="resumeCheckpointButton" disabled data-i18n="timeTravel.resumeCheckpoint">从 checkpoint 恢复</button>
        <span class="meta" id="selectedNodeLabel" data-i18n="timeTravel.noNode">未选择节点</span>
      </div>
    </section>
    <section class="panel detail">
      <div class="panel-head"><div class="panel-title" data-i18n="panel.details">详情</div><span class="badge" id="detailType" data-i18n="empty.empty">空</span></div>
      <div class="detail-body" id="details"><div class="empty" data-i18n="empty.selectEvent">请选择事件</div></div>
    </section>
  </main>
  <script>
    const I18N = {
      'zh-CN': {
        'app.title': 'AgentFlow 可观测性',
        'filter.all': '全部运行', 'filter.running': '运行中', 'filter.paused': '已暂停', 'filter.completed': '已完成', 'filter.failed': '失败', 'filter.aria': '状态筛选',
        'action.refresh': '刷新', 'action.liveOn': '实时开', 'action.liveOff': '实时关',
        'panel.sessions': '会话', 'panel.runView': '运行视图', 'panel.details': '详情',
        'stat.events': '事件', 'stat.tools': '工具调用', 'stat.llm': 'LLM 调用',
        'tab.timeline': '时间线', 'tab.graph': '图', 'tab.editor': '编辑器', 'tab.compare': '对比', 'tab.thread': '线程',
        'empty.noSessions': '暂无会话', 'empty.selectSession': '请选择会话', 'empty.noRun': '未选择运行', 'empty.selectEvent': '请选择事件', 'empty.empty': '空',
        'empty.noEvents': '暂无事件', 'empty.noGraph': '无工作流图',
        'empty.graphRequiresFramework': '需要在 ObservabilityHTTPHandlerConfig.Framework 中接入 Framework 才能导出图',
        'empty.graphRequiresFrameworkShort': '需要接入 Framework 才能导出图',
        'empty.loadGraphToEdit': '加载图以开始编辑', 'empty.selectCompareRun': '请选择运行并选择对比目标', 'empty.chooseCompareTarget': '选择对比目标并点击「对比」',
        'empty.selectRunForThread': '请选择运行以查看分叉线程', 'empty.selectRun': '请选择运行', 'empty.noThreadRuns': '暂无线程运行', 'empty.threadUnavailable': '线程不可用',
        'empty.noCheckpoints': '暂无 checkpoint 记录', 'empty.checkpointRequiresHistory': '需要配置 WithCheckpointHistory 才能查看 checkpoint 历史', 'empty.selectEventOrCheckpoint': '请选择事件或 checkpoint',
        'editor.addSubgraph': '添加子图', 'editor.addNode': '添加节点', 'editor.undo': '撤销', 'editor.redo': '重做',         'editor.connect': '连边', 'editor.fromNode': '从 {node}', 'editor.deleteEdge': '删除边',
        'editor.delete': '删除', 'editor.validate': '校验', 'editor.exportYaml': '导出 YAML', 'editor.importYaml': '导入 YAML', 'editor.previewSave': '预览保存', 'editor.saveScenario': '保存场景',
        'editor.revertLoaded': '恢复到已加载', 'editor.exportGo': '导出 Go', 'editor.runGraph': '运行图', 'editor.quickAdd': '快速添加：',
        'editor.nodeProps': '所选节点属性', 'editor.edgeProps': '所选边', 'editor.applyNodeProps': '应用节点属性', 'editor.applyEdgeProps': '应用边属性',
        'editor.runPromptPlaceholder': 'Studio 运行提示词（可选）', 'editor.refPlaceholder': 'ref（agent/tool/skill/subgraph）',
        'editor.nodeConditionPlaceholder': '节点 condition，例如 steps.a.output.ok', 'editor.dependsOnPlaceholder': 'depends_on 逗号分隔，例如 prep,review',
        'editor.inputPlaceholder': 'input JSON，例如 {"set":{"x":1}}', 'editor.interrupt': 'Declarative interrupt（执行后暂停）', 'editor.edgeConditionPlaceholder': '边 condition，例如 steps.flag.output.ok', 'editor.canvasAria': '编辑器画布',
        'compare.labelB': '对比 B', 'compare.button': '对比', 'compare.onlyA': '仅 A：', 'compare.onlyB': '仅 B：', 'compare.shared': '共享：',
        'thread.fork': '分叉当前运行', 'thread.refresh': '刷新线程', 'thread.thread': '线程', 'thread.forkOf': '分叉自', 'thread.root': '根',
        'timeTravel.resumeStep': '从所选节点恢复', 'timeTravel.resumeCheckpoint': '从 checkpoint 恢复', 'timeTravel.noNode': '未选择节点',
        'timeTravel.nodeLabel': '节点：{node}{hint}', 'timeTravel.notResumable': '此节点无法从步骤恢复',
        'hitl.approve': '批准并继续', 'hitl.reject': '拒绝', 'hitl.pending': '等待审批：{node}（{kind}）', 'hitl.interrupt': 'declarative interrupt', 'hitl.humanGate': 'human gate',
        'detail.run': '运行', 'detail.scenario': '场景', 'detail.sequence': '序号', 'detail.occurred': '发生时间', 'detail.stored': '入库时间',
        'detail.version': '版本', 'detail.status': '状态', 'detail.steps': '步骤', 'detail.recorded': '记录时间', 'detail.current': '当前节点',
        'detail.checkpoint': 'Checkpoint', 'detail.codegen': '代码生成', 'detail.yaml': '场景 YAML', 'detail.savePreview': '保存预览', 'detail.checkpointHistory': 'Checkpoint 历史', 'detail.stepsCount': '{n} 步',
        'run.events': '{n} 个事件', 'run.scenarioFallback': '场景', 'graph.subgraph': '子图：{name}', 'graph.mode': '{name}（{mode}）',
        'editor.workflowOption': '工作流：{name}', 'editor.subgraphOption': '子图：{name}',
        'prompt.subgraphName': '子图名称', 'prompt.edgeCondition': '边 condition（可选）', 'prompt.nodeId': '节点 ID', 'prompt.refOptional': 'Ref（可选）',
        'alert.subgraphExists': '子图已存在', 'alert.nodeExists': '节点已存在', 'alert.invalidJson': 'input 必须是合法 JSON',
        'alert.graphValid': '图校验通过', 'alert.invalidGraph': '图无效', 'alert.codegenFailed': '代码生成失败', 'alert.yamlFailed': 'YAML 导出失败', 'alert.importYamlFailed': 'YAML 导入失败',
        'alert.previewFailed': '预览失败', 'alert.saveFailed': '保存失败', 'alert.runFailed': 'Studio 运行失败', 'alert.compareFailed': '对比失败',
        'alert.forkFailed': '分叉失败', 'alert.resumeFailed': '恢复失败', 'alert.hitlFailed': 'HITL 审批失败', 'confirm.saveGraph': '将编辑后的图保存到宿主场景文件？',
        'confirm.revertGraph': '丢弃本地编辑并重新加载已加载的场景图？', 'alert.savedTo': '已保存到 {path}', 'alert.scenarioFile': '场景文件', 'alert.reloadFailed': '重新加载图失败',
        'errors.studio.save_path_missing': '未配置场景保存路径', 'errors.studio.graph_required': '缺少 graph 数据',
        'errors.graph.duplicate_node': '重复节点 {id}', 'errors.graph.invalid': '图无效', 'errors.studio.internal': '内部错误',
        'errors.studio.invalid_json': '请求 JSON 无效', 'errors.studio.run_state_missing': '未配置 run-state',
        'errors.studio.checkpoint_missing': '未配置 checkpoint 历史', 'errors.studio.compare_runs_missing': '需要 run_a 与 run_b',
        'errors.studio.unsupported_mode': 'Studio 运行仅支持 fixed_workflow 与 hybrid',
        'errors.obs.node_id_required': '需要 node_id', 'errors.obs.version_required': 'version 必须为正整数',
        'errors.obs.streaming_unsupported': '不支持流式传输',
        'errors.obs.not_configured': '未配置 {feature}',
        'obs.feature.graph_export': '图导出', 'obs.feature.run_steps': '步骤列表', 'obs.feature.resume_from_step': '从步骤恢复',
        'obs.feature.checkpoint_history': 'checkpoint 历史', 'obs.feature.checkpoint_loading': 'checkpoint 加载',
        'obs.feature.resume_from_checkpoint': '从 checkpoint 恢复', 'obs.feature.run_compare': '运行对比',
        'obs.feature.studio_validate': 'Studio 校验', 'obs.feature.studio_codegen': 'Studio 代码生成',
        'obs.feature.studio_yaml': 'Studio YAML', 'obs.feature.studio_import_yaml': 'Studio YAML 导入', 'obs.feature.studio_run': 'Studio 运行', 'obs.feature.studio_save': 'Studio 保存',
        'obs.feature.run_thread': '运行线程', 'obs.feature.run_fork': '运行分叉', 'obs.feature.feature': '功能',
        'status.running': '运行中', 'status.paused': '已暂停', 'status.completed': '已完成', 'status.failed': '失败'
      },
      'en': {
        'app.title': 'AgentFlow Observability',
        'filter.all': 'All runs', 'filter.running': 'Running', 'filter.paused': 'Paused', 'filter.completed': 'Completed', 'filter.failed': 'Failed', 'filter.aria': 'Status filter',
        'action.refresh': 'Refresh', 'action.liveOn': 'Live on', 'action.liveOff': 'Live off',
        'panel.sessions': 'Sessions', 'panel.runView': 'Run View', 'panel.details': 'Details',
        'stat.events': 'events', 'stat.tools': 'tool calls', 'stat.llm': 'LLM calls',
        'tab.timeline': 'Timeline', 'tab.graph': 'Graph', 'tab.editor': 'Editor', 'tab.compare': 'Compare', 'tab.thread': 'Thread',
        'empty.noSessions': 'No sessions', 'empty.selectSession': 'Select a session', 'empty.noRun': 'No run', 'empty.selectEvent': 'Select an event', 'empty.empty': 'Empty',
        'empty.noEvents': 'No events', 'empty.noGraph': 'No workflow graph',
        'empty.graphRequiresFramework': 'Graph export requires Framework wiring on ObservabilityHTTPHandlerConfig.Framework',
        'empty.graphRequiresFrameworkShort': 'Graph export requires Framework wiring',
        'empty.loadGraphToEdit': 'Load graph to edit', 'empty.selectCompareRun': 'Select a run and choose compare target', 'empty.chooseCompareTarget': 'Choose compare target and click Compare',
        'empty.selectRunForThread': 'Select a run to view fork thread', 'empty.selectRun': 'Select a run', 'empty.noThreadRuns': 'No thread runs', 'empty.threadUnavailable': 'thread unavailable',
        'empty.noCheckpoints': 'No checkpoints recorded', 'empty.checkpointRequiresHistory': 'Checkpoint history requires WithCheckpointHistory wiring', 'empty.selectEventOrCheckpoint': 'Select an event or checkpoint',
        'editor.addSubgraph': 'Add subgraph', 'editor.addNode': 'Add node', 'editor.undo': 'Undo', 'editor.redo': 'Redo',         'editor.connect': 'Connect', 'editor.fromNode': 'From {node}', 'editor.deleteEdge': 'Delete edge',
        'editor.delete': 'Delete', 'editor.validate': 'Validate', 'editor.exportYaml': 'Export YAML', 'editor.importYaml': 'Import YAML', 'editor.previewSave': 'Preview save', 'editor.saveScenario': 'Save scenario',
        'editor.revertLoaded': 'Revert to loaded', 'editor.exportGo': 'Export Go', 'editor.runGraph': 'Run graph', 'editor.quickAdd': 'Quick add:',
        'editor.nodeProps': 'Selected node properties', 'editor.edgeProps': 'Selected edge', 'editor.applyNodeProps': 'Apply node properties', 'editor.applyEdgeProps': 'Apply edge properties',
        'editor.runPromptPlaceholder': 'Prompt for studio run (optional)', 'editor.refPlaceholder': 'ref (agent/tool/skill/subgraph)',
        'editor.nodeConditionPlaceholder': 'node condition e.g. steps.a.output.ok', 'editor.dependsOnPlaceholder': 'depends_on comma-separated e.g. prep,review',
        'editor.inputPlaceholder': 'input JSON e.g. {"set":{"x":1}}', 'editor.interrupt': 'Declarative interrupt (pause after step)', 'editor.edgeConditionPlaceholder': 'edge condition e.g. steps.flag.output.ok', 'editor.canvasAria': 'Editor canvas',
        'compare.labelB': 'Compare B', 'compare.button': 'Compare', 'compare.onlyA': 'Only A:', 'compare.onlyB': 'Only B:', 'compare.shared': 'Shared:',
        'thread.fork': 'Fork current run', 'thread.refresh': 'Refresh thread', 'thread.thread': 'thread', 'thread.forkOf': 'fork of', 'thread.root': 'root',
        'timeTravel.resumeStep': 'Resume from selected node', 'timeTravel.resumeCheckpoint': 'Resume from checkpoint', 'timeTravel.noNode': 'No node selected',
        'timeTravel.nodeLabel': 'Node: {node}{hint}', 'timeTravel.notResumable': 'This node cannot be resumed from step',
        'hitl.approve': 'Approve & continue', 'hitl.reject': 'Reject', 'hitl.pending': 'Awaiting approval: {node} ({kind})', 'hitl.interrupt': 'declarative interrupt', 'hitl.humanGate': 'human gate',
        'detail.run': 'Run', 'detail.scenario': 'Scenario', 'detail.sequence': 'Sequence', 'detail.occurred': 'Occurred', 'detail.stored': 'Stored',
        'detail.version': 'Version', 'detail.status': 'Status', 'detail.steps': 'Steps', 'detail.recorded': 'Recorded', 'detail.current': 'Current',
        'detail.checkpoint': 'Checkpoint', 'detail.codegen': 'Codegen', 'detail.yaml': 'Scenario YAML', 'detail.savePreview': 'Save preview', 'detail.checkpointHistory': 'Checkpoint history', 'detail.stepsCount': '{n} steps',
        'run.events': '{n} events', 'run.scenarioFallback': 'scenario', 'graph.subgraph': 'subgraph: {name}', 'graph.mode': '{name} ({mode})',
        'editor.workflowOption': 'workflow: {name}', 'editor.subgraphOption': 'subgraph: {name}',
        'prompt.subgraphName': 'Subgraph name', 'prompt.edgeCondition': 'Edge condition (optional)', 'prompt.nodeId': 'Node id', 'prompt.refOptional': 'Ref (optional)',
        'alert.subgraphExists': 'subgraph already exists', 'alert.nodeExists': 'node already exists', 'alert.invalidJson': 'input must be valid JSON',
        'alert.graphValid': 'Graph is valid', 'alert.invalidGraph': 'invalid graph', 'alert.codegenFailed': 'codegen failed', 'alert.yamlFailed': 'yaml export failed', 'alert.importYamlFailed': 'yaml import failed',
        'alert.previewFailed': 'preview failed', 'alert.saveFailed': 'save failed', 'alert.runFailed': 'studio run failed', 'alert.compareFailed': 'compare failed',
        'alert.forkFailed': 'fork failed', 'alert.resumeFailed': 'resume failed', 'alert.hitlFailed': 'HITL decision failed', 'confirm.saveGraph': 'Save edited graph back to the host scenario file?',
        'confirm.revertGraph': 'Discard local edits and reload the loaded scenario graph?', 'alert.savedTo': 'Saved to {path}', 'alert.scenarioFile': 'scenario file', 'alert.reloadFailed': 'Failed to reload graph',
        'errors.studio.save_path_missing': 'Studio save path is not configured', 'errors.studio.graph_required': 'Graph is required',
        'errors.graph.duplicate_node': 'Duplicate node {id}', 'errors.graph.invalid': 'Invalid graph', 'errors.studio.internal': 'Internal error',
        'errors.studio.invalid_json': 'Invalid request JSON', 'errors.studio.run_state_missing': 'Run-state repository is not configured',
        'errors.studio.checkpoint_missing': 'Checkpoint history is not configured', 'errors.studio.compare_runs_missing': 'run_a and run_b are required',
        'errors.studio.unsupported_mode': 'Studio run supports fixed_workflow and hybrid only',
        'errors.obs.node_id_required': 'node_id is required', 'errors.obs.version_required': 'version must be a positive integer',
        'errors.obs.streaming_unsupported': 'Streaming is not supported',
        'errors.obs.not_configured': '{feature} is not configured',
        'obs.feature.graph_export': 'Graph export', 'obs.feature.run_steps': 'Run steps', 'obs.feature.resume_from_step': 'Resume from step',
        'obs.feature.checkpoint_history': 'Checkpoint history', 'obs.feature.checkpoint_loading': 'Checkpoint loading',
        'obs.feature.resume_from_checkpoint': 'Resume from checkpoint', 'obs.feature.run_compare': 'Run compare',
        'obs.feature.studio_validate': 'Studio validate', 'obs.feature.studio_codegen': 'Studio codegen',
        'obs.feature.studio_yaml': 'Studio YAML', 'obs.feature.studio_import_yaml': 'Studio YAML import', 'obs.feature.studio_run': 'Studio run', 'obs.feature.studio_save': 'Studio save',
        'obs.feature.run_thread': 'Run thread', 'obs.feature.run_fork': 'Run fork', 'obs.feature.feature': 'Feature',
        'status.running': 'running', 'status.paused': 'paused', 'status.completed': 'completed', 'status.failed': 'failed'
      }
    };
    let locale = localStorage.getItem('obs-lang') || 'zh-CN';
    const state = { runs: [], events: [], selectedRun: '', selectedEvent: null, stream: null, live: true, view: 'timeline', graph: null, editorGraph: null, editorTarget: 'workflow', editorPositions: {}, editorConnectFrom: '', editorDrag: null, editorEdgeDrag: null, editorHistory: [], editorHistoryIndex: -1, selectedEdge: null, selectedNodes: [], steps: null, checkpoints: null, selectedNode: '', selectedCheckpoint: null, graphEnabled: false, resumeEnabled: false, hitlEnabled: false, checkpointEnabled: false, activeSubgraphs: {}, nodeMeta: {}, compareRunB: '', compareResult: null, threadRuns: [] };
    const t = (key, vars) => {
      let text = (I18N[locale] && I18N[locale][key]) || (I18N.en && I18N.en[key]) || key;
      if (vars) Object.keys(vars).forEach((name) => { text = text.split('{' + name + '}').join(String(vars[name])); });
      return text;
    };
    const statusLabel = (status) => {
      const key = 'status.' + status;
      return (I18N[locale] && I18N[locale][key]) ? t(key) : status;
    };
    function formatApiError(body, fallbackKey) {
      const err = body && body.error;
      if (!err) return t(fallbackKey);
      if (typeof err === 'string') return err;
      const code = err.code || '';
      const i18nKey = 'errors.' + code;
      if (code && I18N[locale] && I18N[locale][i18nKey]) {
        const vars = Object.assign({}, err.params || {});
        if (code === 'obs.not_configured' && vars.feature) {
          vars.feature = t('obs.feature.' + vars.feature, vars);
        }
        return t(i18nKey, vars);
      }
      return err.message || t(fallbackKey);
    }
    function isNodeSelected(nodeID) {
      return state.selectedNode === nodeID || (state.selectedNodes || []).includes(nodeID);
    }
    function setNodeSelection(nodeID, additive) {
      if (!additive) {
        state.selectedNodes = nodeID ? [nodeID] : [];
        state.selectedNode = nodeID || '';
        return;
      }
      const selected = new Set(state.selectedNodes || []);
      if (state.selectedNode) selected.add(state.selectedNode);
      if (selected.has(nodeID)) selected.delete(nodeID); else selected.add(nodeID);
      state.selectedNodes = Array.from(selected);
      state.selectedNode = state.selectedNodes[0] || '';
    }
    function applyStaticI18n() {
      document.documentElement.lang = locale;
      document.title = t('app.title');
      document.querySelectorAll('[data-i18n]').forEach((el) => {
        if (el.id === 'selectedRun' || el.id === 'detailType' || el.id === 'selectedNodeLabel') return;
        el.textContent = t(el.getAttribute('data-i18n'));
      });
      document.querySelectorAll('[data-i18n-placeholder]').forEach((el) => {
        el.placeholder = t(el.getAttribute('data-i18n-placeholder'));
      });
      document.querySelectorAll('[data-i18n-aria]').forEach((el) => {
        el.setAttribute('aria-label', t(el.getAttribute('data-i18n-aria')));
      });
      updateLiveButton();
      updateConnectButton();
    }
    function updateLiveButton() {
      $('liveButton').textContent = state.live ? t('action.liveOn') : t('action.liveOff');
    }
    function updateConnectButton() {
      if (!$('connectModeButton')) return;
      $('connectModeButton').textContent = state.editorConnectFrom ? t('editor.fromNode', { node: state.editorConnectFrom }) : t('editor.connect');
    }
    function setLocale(next) {
      locale = next || 'zh-CN';
      localStorage.setItem('obs-lang', locale);
      $('langSelect').value = locale;
      applyStaticI18n();
      renderRuns();
      renderEvents();
      renderDetails();
      updateTimeTravelBar();
      if (state.view === 'graph') renderGraph();
      if (state.view === 'editor') { renderEditorTargetOptions(); renderEditor(); }
      if (state.view === 'compare') renderCompare();
      if (state.view === 'thread') renderThread();
    }
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
    async function loadGraph(options) {
      options = options || {};
      try {
        const res = await fetch('api/graph');
        if (!res.ok) {
          if (options.alertOnError) alert(t('alert.reloadFailed'));
          return false;
        }
        state.graph = await res.json();
        state.graphEnabled = true;
        state.nodeMeta = buildNodeMeta(state.graph);
        resetEditorGraph({ preserveTarget: options.preserveTarget === true });
        $('timeTravelBar').hidden = false;
        if (state.view === 'graph') renderGraph();
        return true;
      } catch (_) {
        if (options.alertOnError) alert(t('alert.reloadFailed'));
        return false;
      }
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
        state.hitlEnabled = true;
      } catch (_) {}
      renderGraph();
      updateTimeTravelBar();
      updateHitlBar();
    }
    function resetEditorGraph(options) {
      options = options || {};
      if (!state.graph) return;
      const preserveTarget = options.preserveTarget === true;
      const target = preserveTarget ? state.editorTarget : 'workflow';
      const selectedNode = preserveTarget ? state.selectedNode : '';
      const selectedNodes = preserveTarget ? (state.selectedNodes || []) : [];
      state.editorGraph = JSON.parse(JSON.stringify(state.graph));
      state.editorPositions = {};
      state.editorConnectFrom = '';
      state.editorTarget = target;
      state.selectedEdge = null;
      state.selectedNode = selectedNode;
      state.selectedNodes = selectedNodes.slice();
      state.editorHistory = [];
      state.editorHistoryIndex = -1;
      hydrateEditorLayout(state.editorGraph.workflow);
      Object.values(state.editorGraph.workflows || {}).forEach((view) => hydrateEditorLayout(view));
      renderEditorTargetOptions();
      updateEditorHistoryButtons();
      pushEditorHistory();
    }
    function hydrateEditorLayout(view) {
      if (!view || !view.layout) return;
      Object.entries(view.layout).forEach(([id, pos]) => {
        state.editorPositions[id] = { x: Number(pos.x) || 0, y: Number(pos.y) || 0 };
      });
    }
    function syncEditorLayout(view) {
      if (!view) return;
      const layout = {};
      (view.nodes || []).forEach((node) => {
        const pos = state.editorPositions[node.id];
        if (pos) layout[node.id] = { x: pos.x, y: pos.y };
      });
      view.layout = Object.keys(layout).length ? layout : undefined;
    }
    function syncAllEditorLayouts() {
      if (!state.editorGraph) return;
      syncEditorLayout(state.editorGraph.workflow);
      Object.values(state.editorGraph.workflows || {}).forEach((view) => syncEditorLayout(view));
    }
    function editorGraphPayload() {
      syncAllEditorLayouts();
      return state.editorGraph;
    }
    function snapshotEditorState() {
      syncAllEditorLayouts();
      return JSON.stringify({
        graph: state.editorGraph,
        positions: state.editorPositions,
        target: state.editorTarget,
        selectedNode: state.selectedNode,
        selectedEdge: state.selectedEdge,
      });
    }
    function restoreEditorState(raw) {
      const saved = JSON.parse(raw);
      state.editorGraph = saved.graph;
      state.editorPositions = saved.positions || {};
      state.editorTarget = saved.target || 'workflow';
      state.selectedNode = saved.selectedNode || '';
      state.selectedEdge = saved.selectedEdge || null;
      renderEditorTargetOptions();
      renderEditor();
    }
    function pushEditorHistory() {
      if (!state.editorGraph) return;
      const snap = snapshotEditorState();
      if (state.editorHistoryIndex >= 0 && state.editorHistory[state.editorHistoryIndex] === snap) return;
      if (state.editorHistoryIndex < state.editorHistory.length - 1) {
        state.editorHistory = state.editorHistory.slice(0, state.editorHistoryIndex + 1);
      }
      state.editorHistory.push(snap);
      if (state.editorHistory.length > 50) state.editorHistory.shift();
      state.editorHistoryIndex = state.editorHistory.length - 1;
      updateEditorHistoryButtons();
    }
    function updateEditorHistoryButtons() {
      $('undoEditorButton').disabled = state.editorHistoryIndex <= 0;
      $('redoEditorButton').disabled = state.editorHistoryIndex < 0 || state.editorHistoryIndex >= state.editorHistory.length - 1;
    }
    function undoEditor() {
      if (state.editorHistoryIndex <= 0) return;
      state.editorHistoryIndex -= 1;
      restoreEditorState(state.editorHistory[state.editorHistoryIndex]);
      updateEditorHistoryButtons();
    }
    function redoEditor() {
      if (state.editorHistoryIndex >= state.editorHistory.length - 1) return;
      state.editorHistoryIndex += 1;
      restoreEditorState(state.editorHistory[state.editorHistoryIndex]);
      updateEditorHistoryButtons();
    }
    function renderEditorTargetOptions() {
      const select = $('editorTargetSelect');
      if (!select || !state.editorGraph) return;
      const options = [{ value: 'workflow', label: t('editor.workflowOption', { name: state.editorGraph.name || 'main' }) }];
      Object.keys(state.editorGraph.workflows || {}).sort().forEach((name) => {
        options.push({ value: name, label: t('editor.subgraphOption', { name }) });
      });
      select.innerHTML = options.map((item) =>
        '<option value="' + escapeHTML(item.value) + '"' + (item.value === state.editorTarget ? ' selected' : '') + '>' + escapeHTML(item.label) + '</option>'
      ).join('');
    }
    function switchEditorTarget() {
      syncEditorLayout(editorView());
      state.editorTarget = $('editorTargetSelect').value;
      state.editorPositions = {};
      state.selectedNode = '';
      state.selectedEdge = null;
      hydrateEditorLayout(editorView());
      renderEditor();
    }
    function editorView() {
      if (!state.editorGraph) return null;
      if (state.editorTarget === 'workflow') {
        return state.editorGraph.workflow || null;
      }
      if (!state.editorGraph.workflows) state.editorGraph.workflows = {};
      if (!state.editorGraph.workflows[state.editorTarget]) {
        state.editorGraph.workflows[state.editorTarget] = { id: state.editorTarget, nodes: [], edges: [] };
      }
      return state.editorGraph.workflows[state.editorTarget];
    }
    function setView(view) {
      state.view = view;
      $('timelineTab').classList.toggle('active', view === 'timeline');
      $('graphTab').classList.toggle('active', view === 'graph');
      $('editorTab').classList.toggle('active', view === 'editor');
      $('compareTab').classList.toggle('active', view === 'compare');
      $('threadTab').classList.toggle('active', view === 'thread');
      $('events').hidden = view !== 'timeline';
      $('graphView').hidden = view !== 'graph';
      $('editorView').hidden = view !== 'editor';
      $('compareView').hidden = view !== 'compare';
      $('threadView').hidden = view !== 'thread';
      $('timeTravelBar').hidden = view !== 'graph';
      if (view === 'graph') renderGraph();
      if (view === 'editor') renderEditor();
      if (view === 'compare') { renderCompareRunOptions(); renderCompare(); }
      if (view === 'thread') loadThread();
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
        container.innerHTML = '<div class="empty">' + escapeHTML(t('empty.noGraph')) + '</div>';
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
        html += '<text x="8" y="34" fill="#697586">' + escapeHTML(node.kind + (node.ref ? ':' + node.ref : '') + (node.interrupt ? ' ⏸' : '')) + '</text>';
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
        container.innerHTML = '<div class="empty">' + escapeHTML(t('empty.graphRequiresFramework')) + '</div>';
        return;
      }
      let html = '';
      const root = document.createElement('div');
      renderGraphView(root, state.graph.workflow, t('graph.mode', { name: state.graph.name, mode: state.graph.mode || 'mode' }));
      html += root.innerHTML;
      if (state.graph.workflows) {
        Object.entries(state.graph.workflows).forEach(([name, view]) => {
          const sub = document.createElement('div');
          sub.className = 'graph-sub';
          renderGraphView(sub, view, t('graph.subgraph', { name }));
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
    function editorWorkflow() {
      return editorView();
    }
    function renderEditorNodeProps() {
      const panel = $('editorProps');
      const edgePanel = $('editorEdgeProps');
      const view = editorView();
      if (edgePanel) edgePanel.hidden = !state.selectedEdge;
      if (state.selectedEdge && edgePanel) {
        $('editorEdgeFrom').value = state.selectedEdge.from;
        $('editorEdgeTo').value = state.selectedEdge.to;
        const edge = (view && view.edges || []).find((item) => item.from === state.selectedEdge.from && item.to === state.selectedEdge.to);
        $('editorEdgeCondition').value = edge && edge.condition ? edge.condition : '';
      }
      if (!panel || !view || !state.selectedNode || state.selectedEdge) {
        if (panel) panel.hidden = true;
        return;
      }
      const node = (view.nodes || []).find((item) => item.id === state.selectedNode);
      if (!node) { panel.hidden = true; return; }
      panel.hidden = false;
      $('editorNodeRef').value = node.ref || '';
      $('editorNodeCondition').value = node.condition || '';
      $('editorNodeDependsOn').value = (node.depends_on || []).join(', ');
      $('editorNodeInput').value = node.input ? (typeof node.input === 'string' ? node.input : JSON.stringify(node.input, null, 2)) : '';
      $('editorNodeInterrupt').checked = !!node.interrupt;
    }
    function applyEditorNodeProps() {
      const view = editorView();
      if (!view || !state.selectedNode) return;
      const node = (view.nodes || []).find((item) => item.id === state.selectedNode);
      if (!node) return;
      pushEditorHistory();
      node.ref = $('editorNodeRef').value.trim();
      node.condition = $('editorNodeCondition').value.trim();
      const dependsRaw = $('editorNodeDependsOn').value.trim();
      if (!dependsRaw) {
        delete node.depends_on;
      } else {
        node.depends_on = dependsRaw.split(',').map((item) => item.trim()).filter(Boolean);
      }
      const raw = $('editorNodeInput').value.trim();
      if (!raw) {
        delete node.input;
      } else {
        try {
          node.input = JSON.parse(raw);
        } catch (_) {
          alert(t('alert.invalidJson'));
          return;
        }
      }
      if ($('editorNodeInterrupt').checked) node.interrupt = true;
      else delete node.interrupt;
      renderEditor();
    }
    function applyEditorEdgeProps() {
      const view = editorView();
      if (!view || !state.selectedEdge) return;
      const edge = (view.edges || []).find((item) => item.from === state.selectedEdge.from && item.to === state.selectedEdge.to);
      if (!edge) return;
      pushEditorHistory();
      const cond = $('editorEdgeCondition').value.trim();
      if (cond) edge.condition = cond; else delete edge.condition;
      renderEditor();
    }
    function addEditorSubgraph() {
      if (!state.editorGraph) return;
      const name = prompt(t('prompt.subgraphName'));
      if (!name) return;
      pushEditorHistory();
      if (!state.editorGraph.workflows) state.editorGraph.workflows = {};
      if (state.editorGraph.workflows[name]) { alert(t('alert.subgraphExists')); return; }
      state.editorGraph.workflows[name] = { id: name, nodes: [], edges: [] };
      state.editorTarget = name;
      renderEditorTargetOptions();
      renderEditor();
    }
    function layoutEditorGraph(view) {
      const base = layoutGraph(view);
      Object.keys(state.editorPositions).forEach((id) => {
        if (base.positions[id]) base.positions[id] = state.editorPositions[id];
      });
      return base;
    }
    function renderEditor() {
      const canvas = $('editorCanvas');
      const view = editorWorkflow();
      if (!state.graphEnabled || !view) {
        canvas.innerHTML = '<div class="empty">' + escapeHTML(t('empty.graphRequiresFrameworkShort')) + '</div>';
        return;
      }
      const { nodes, edges, positions } = layoutEditorGraph(view);
      const width = Math.max(640, ...Object.values(positions).map((p) => p.x + 160));
      const height = Math.max(320, ...Object.values(positions).map((p) => p.y + 70));
      let html = '<svg class="graph-svg" id="editorSvg" viewBox="0 0 ' + width + ' ' + height + '" width="' + width + '" height="' + height + '">';
      html += '<defs><marker id="arrow-editor" markerWidth="8" markerHeight="8" refX="7" refY="3" orient="auto"><path d="M0,0 L7,3 L0,6 Z" fill="#94a3b8"/></marker></defs>';
      edges.forEach((edge) => {
        const from = positions[edge.from];
        const to = positions[edge.to];
        if (!from || !to) return;
        const selected = state.selectedEdge && state.selectedEdge.from === edge.from && state.selectedEdge.to === edge.to;
        html += '<path class="graph-edge' + (selected ? ' active' : '') + '" data-from="' + escapeHTML(edge.from) + '" data-to="' + escapeHTML(edge.to) + '" marker-end="url(#arrow-editor)" d="M' + (from.x + 120) + ',' + (from.y + 24) + ' C' + (from.x + 150) + ',' + from.y + ' ' + (to.x - 20) + ',' + to.y + ' ' + to.x + ',' + (to.y + 24) + '"/>';
        if (edge.condition) {
          const mx = (from.x + to.x) / 2 + 60;
          const my = (from.y + to.y) / 2 + 16;
          html += '<text class="graph-edge-label" x="' + mx + '" y="' + my + '" font-size="10" fill="#64748b">' + escapeHTML(edge.condition) + '</text>';
        }
      });
      nodes.forEach((node) => {
        const pos = positions[node.id];
        const classes = ['graph-node', 'editing'];
        if (state.selectedNode === node.id) classes.push('active');
        if (isNodeSelected(node.id) && (state.selectedNodes || []).length > 1) classes.push('multi-selected');
        html += '<g class="' + classes.join(' ') + '" data-node="' + escapeHTML(node.id) + '" transform="translate(' + pos.x + ',' + pos.y + ')">';
        html += '<rect width="120" height="48" rx="8"></rect>';
        html += '<text x="8" y="18" font-weight="700">' + escapeHTML(node.id) + '</text>';
        html += '<text x="8" y="34" fill="#697586">' + escapeHTML(node.kind + (node.ref ? ':' + node.ref : '') + (node.interrupt ? ' ⏸' : '')) + '</text>';
        html += '<circle class="graph-port" cx="120" cy="24" r="5" data-port="' + escapeHTML(node.id) + '"></circle>';
        html += '</g>';
      });
      html += '</svg>';
      canvas.innerHTML = html;
      canvas.querySelectorAll('.graph-edge').forEach((edgeEl) => edgeEl.onclick = (event) => {
        event.stopPropagation();
        state.selectedNode = '';
        state.selectedNodes = [];
        state.selectedEdge = { from: edgeEl.dataset.from, to: edgeEl.dataset.to };
        renderEditor();
      });
      canvas.querySelectorAll('.graph-port').forEach((portEl) => {
        portEl.onmousedown = (event) => {
          event.stopPropagation();
          state.editorEdgeDrag = { from: portEl.dataset.port };
        };
      });
      canvas.querySelectorAll('.graph-node').forEach((nodeEl) => {
        nodeEl.onmousedown = (event) => {
          if (event.target.classList.contains('graph-port')) return;
          startEditorDrag(event, nodeEl);
        };
        nodeEl.onmouseup = (event) => {
          if (!state.editorEdgeDrag || state.editorEdgeDrag.from === nodeEl.dataset.node) return;
          finishEdgeDrag(nodeEl.dataset.node);
          event.stopPropagation();
        };
        nodeEl.onclick = (event) => {
          event.stopPropagation();
          if (state.editorEdgeDrag) return;
          handleEditorNodeClick(nodeEl.dataset.node, event);
        };
      });
      renderEditorNodeProps();
    }
    function finishEdgeDrag(targetID) {
      const from = state.editorEdgeDrag && state.editorEdgeDrag.from;
      state.editorEdgeDrag = null;
      if (!from || !targetID || from === targetID) return;
      const view = editorWorkflow();
      if (!view) return;
      view.edges = view.edges || [];
      if (view.edges.some((edge) => edge.from === from && edge.to === targetID)) return;
      pushEditorHistory();
      const cond = prompt(t('prompt.edgeCondition'), '') || '';
      const edge = { from, to: targetID };
      if (cond.trim()) edge.condition = cond.trim();
      view.edges.push(edge);
      renderEditor();
    }
    function handleEditorNodeClick(nodeID, event) {
      if (state.editorConnectFrom) {
        const view = editorWorkflow();
        if (view && state.editorConnectFrom !== nodeID) {
          view.edges = view.edges || [];
          if (!view.edges.some((edge) => edge.from === state.editorConnectFrom && edge.to === nodeID)) {
            pushEditorHistory();
            const cond = prompt(t('prompt.edgeCondition'), '') || '';
            const edge = { from: state.editorConnectFrom, to: nodeID };
            if (cond.trim()) edge.condition = cond.trim();
            view.edges.push(edge);
          }
        }
        state.editorConnectFrom = '';
        updateConnectButton();
      } else {
        setNodeSelection(nodeID, event && event.shiftKey);
      }
      state.selectedEdge = null;
      renderEditor();
    }
    function deleteEditorEdge() {
      const view = editorWorkflow();
      if (!view || !state.selectedEdge) return;
      pushEditorHistory();
      view.edges = (view.edges || []).filter((edge) => !(edge.from === state.selectedEdge.from && edge.to === state.selectedEdge.to));
      state.selectedEdge = null;
      renderEditor();
    }
    function startEditorDrag(event, nodeEl) {
      if (state.editorConnectFrom) return;
      event.preventDefault();
      const nodeID = nodeEl.dataset.node;
      if (!isNodeSelected(nodeID)) setNodeSelection(nodeID, false);
      const nodeIDs = (state.selectedNodes && state.selectedNodes.length) ? state.selectedNodes.slice() : [nodeID];
      const svg = $('editorSvg');
      const start = svgPoint(svg, event.clientX, event.clientY);
      const offsets = {};
      nodeIDs.forEach((id) => {
        const pos = state.editorPositions[id] || parseTranslate(nodeEl.getAttribute('transform'));
        offsets[id] = { offsetX: start.x - pos.x, offsetY: start.y - pos.y };
      });
      state.editorDrag = { nodeIDs, offsets, moved: false };
      const move = (ev) => {
        if (!state.editorDrag) return;
        state.editorDrag.moved = true;
        const point = svgPoint(svg, ev.clientX, ev.clientY);
        state.editorDrag.nodeIDs.forEach((id) => {
          const offset = state.editorDrag.offsets[id];
          state.editorPositions[id] = {
            x: Math.max(0, point.x - offset.offsetX),
            y: Math.max(0, point.y - offset.offsetY),
          };
        });
        renderEditor();
      };
      const up = () => {
        if (state.editorDrag && state.editorDrag.moved) pushEditorHistory();
        state.editorDrag = null;
        window.removeEventListener('mousemove', move);
        window.removeEventListener('mouseup', up);
      };
      window.addEventListener('mousemove', move);
      window.addEventListener('mouseup', up);
    }
    function parseTranslate(value) {
      const match = /translate\(([-\d.]+),\s*([-\d.]+)\)/.exec(value || '');
      return { x: match ? Number(match[1]) : 0, y: match ? Number(match[2]) : 0 };
    }
    function svgPoint(svg, clientX, clientY) {
      const pt = svg.createSVGPoint();
      pt.x = clientX;
      pt.y = clientY;
      return pt.matrixTransform(svg.getScreenCTM().inverse());
    }
    function addEditorNode(kindOverride) {
      const view = editorWorkflow();
      if (!view) return;
      const id = prompt(t('prompt.nodeId'));
      if (!id) return;
      const kind = kindOverride || $('editorNodeKind').value;
      const ref = (kind === 'agent' || kind === 'tool' || kind === 'skill' || kind === 'subgraph') ? (prompt(t('prompt.refOptional')) || '') : '';
      view.nodes = view.nodes || [];
      if (view.nodes.some((node) => node.id === id)) { alert(t('alert.nodeExists')); return; }
      pushEditorHistory();
      view.nodes.push({ id, kind, ref, resumable: kind !== 'human_gate' && kind !== 'loop' });
      renderEditor();
    }
    function deleteEditorNode() {
      const view = editorWorkflow();
      if (!view || !state.selectedNode) return;
      pushEditorHistory();
      view.nodes = (view.nodes || []).filter((node) => node.id !== state.selectedNode);
      view.edges = (view.edges || []).filter((edge) => edge.from !== state.selectedNode && edge.to !== state.selectedNode);
      delete state.editorPositions[state.selectedNode];
      state.selectedNode = '';
      renderEditor();
    }
    async function validateEditorGraph() {
      if (!state.editorGraph) return;
      const res = await fetch('api/studio/validate', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(editorGraphPayload()) });
      const body = await res.json();
      alert(body.valid ? t('alert.graphValid') : (body.error_code && I18N[locale]['errors.' + body.error_code] ? t('errors.' + body.error_code) : (body.error || t('alert.invalidGraph'))));
    }
    async function codegenEditorGraph() {
      if (!state.editorGraph) return;
      const res = await fetch('api/studio/codegen', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(editorGraphPayload()) });
      const body = await res.json();
      if (!res.ok) { alert(formatApiError(body, 'alert.codegenFailed')); return; }
      state.selectedEvent = null;
      state.selectedCheckpoint = null;
      $('detailType').textContent = t('detail.codegen');
      $('details').innerHTML = '<pre>' + escapeHTML(body.code || '') + '</pre>';
    }
    async function importEditorYaml(yamlText) {
      const res = await fetch('api/studio/import-yaml', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ yaml: yamlText, layout_graph: editorGraphPayload() }),
      });
      const body = await res.json();
      if (!res.ok) { alert(formatApiError(body, 'alert.importYamlFailed')); return; }
      if (!body.graph) return;
      state.graph = body.graph;
      state.nodeMeta = buildNodeMeta(state.graph);
      resetEditorGraph();
      renderEditor();
      setView('editor');
    }
    async function yamlEditorGraph() {
      if (!state.editorGraph) return;
      const res = await fetch('api/studio/yaml', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(editorGraphPayload()) });
      const body = await res.json();
      if (!res.ok) { alert(formatApiError(body, 'alert.yamlFailed')); return; }
      state.selectedEvent = null;
      state.selectedCheckpoint = null;
      $('detailType').textContent = t('detail.yaml');
      $('details').innerHTML = '<pre>' + escapeHTML(body.code || '') + '</pre>';
    }
    function renderTextDiff(before, after) {
      const left = (before || '').split('\n');
      const right = (after || '').split('\n');
      const max = Math.max(left.length, right.length);
      const lines = [];
      for (let i = 0; i < max; i++) {
        const a = left[i] || '';
        const b = right[i] || '';
        if (a === b) lines.push('  ' + a);
        else {
          if (a) lines.push('- ' + a);
          if (b) lines.push('+ ' + b);
        }
      }
      return lines.join('\n');
    }
    async function previewSaveEditorGraph() {
      if (!state.editorGraph || !state.graph) return;
      const [baseRes, editRes] = await Promise.all([
        fetch('api/studio/yaml', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(state.graph) }),
        fetch('api/studio/yaml', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(editorGraphPayload()) }),
      ]);
      const baseBody = await baseRes.json();
      const editBody = await editRes.json();
      if (!baseRes.ok || !editRes.ok) {
        alert(formatApiError({ error: editBody.error || baseBody.error }, 'alert.previewFailed'));
        return;
      }
      state.selectedEvent = null;
      state.selectedCheckpoint = null;
      $('detailType').textContent = t('detail.savePreview');
      $('details').innerHTML = '<pre>' + escapeHTML(renderTextDiff(baseBody.code || '', editBody.code || '')) + '</pre>';
    }
    async function saveEditorGraph() {
      if (!state.editorGraph) return;
      if (!confirm(t('confirm.saveGraph'))) return;
      const res = await fetch('api/studio/save', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(editorGraphPayload()) });
      const body = await res.json();
      if (!res.ok) { alert(formatApiError(body, 'alert.saveFailed')); return; }
      alert(t('alert.savedTo', { path: body.path || t('alert.scenarioFile') }));
      if (body.graph) {
        state.graph = body.graph;
        state.nodeMeta = buildNodeMeta(state.graph);
        resetEditorGraph({ preserveTarget: true });
        renderEditor();
      } else {
        await loadGraph({ preserveTarget: true, alertOnError: true });
        if (state.view === 'editor') renderEditor();
      }
      setView('editor');
    }
    function revertEditorGraph() {
      if (!state.graph) return;
      if (!confirm(t('confirm.revertGraph'))) return;
      resetEditorGraph();
      renderEditor();
    }
    async function runEditorGraph() {
      if (!state.editorGraph) return;
      $('runEditorGraphButton').disabled = true;
      const res = await fetch('api/studio/run', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ graph: editorGraphPayload(), prompt: $('editorRunPrompt').value.trim() }),
      });
      const body = await res.json();
      $('runEditorGraphButton').disabled = false;
      if (!res.ok) { alert(formatApiError(body, 'alert.runFailed')); return; }
      await loadRuns();
      if (body.run_id) {
        await selectRun(body.run_id);
        setView('timeline');
      }
    }
    function renderCompareRunOptions() {
      $('compareRunB').innerHTML = state.runs.map((run) =>
        '<option value="' + escapeHTML(run.run_id) + '"' + (run.run_id === state.compareRunB ? ' selected' : '') + '>' + escapeHTML(run.run_id) + '</option>'
      ).join('');
    }
    async function compareRuns() {
      if (!state.selectedRun) return;
      state.compareRunB = $('compareRunB').value;
      const res = await fetch('api/compare?run_a=' + encodeURIComponent(state.selectedRun) + '&run_b=' + encodeURIComponent(state.compareRunB));
      const body = await res.json();
      if (!res.ok) { alert(formatApiError(body, 'alert.compareFailed')); return; }
      state.compareResult = body;
      renderCompare();
    }
    function renderCompare() {
      const canvas = $('compareCanvas');
      if (!state.compareResult) {
        canvas.innerHTML = '<div class="empty">' + escapeHTML(t('empty.chooseCompareTarget')) + '</div>';
        return;
      }
      const diff = state.compareResult;
      let html = '<div class="meta"><span>' + escapeHTML(t('compare.onlyA')) + ' ' + (diff.steps_only_a || []).join(', ') + '</span></div>';
      html += '<div class="meta"><span>' + escapeHTML(t('compare.onlyB')) + ' ' + (diff.steps_only_b || []).join(', ') + '</span></div>';
      html += '<div class="meta"><span>' + escapeHTML(t('compare.shared')) + ' ' + (diff.shared_steps || []).length + '</span></div>';
      const changed = new Set((diff.shared_steps || []).filter((step) => !step.same).map((step) => step.node_id));
      const onlyA = new Set(diff.steps_only_a || []);
      const onlyB = new Set(diff.steps_only_b || []);
      if (state.graph && state.graph.workflow) {
        const root = document.createElement('div');
        renderDiffGraphView(root, state.graph.workflow, changed, onlyA, onlyB);
        html += root.innerHTML;
      }
      canvas.innerHTML = html;
    }
    function renderDiffGraphView(container, view, changed, onlyA, onlyB) {
      const { nodes, edges, positions } = layoutGraph(view);
      const width = Math.max(640, ...Object.values(positions).map((p) => p.x + 160));
      const height = Math.max(320, ...Object.values(positions).map((p) => p.y + 70));
      let html = '<svg class="graph-svg" viewBox="0 0 ' + width + ' ' + height + '" width="' + width + '" height="' + height + '">';
      edges.forEach((edge) => {
        const from = positions[edge.from]; const to = positions[edge.to];
        if (!from || !to) return;
        html += '<path class="graph-edge" d="M' + (from.x + 120) + ',' + (from.y + 24) + ' C' + (from.x + 150) + ',' + from.y + ' ' + (to.x - 20) + ',' + to.y + ' ' + to.x + ',' + (to.y + 24) + '"/>';
      });
      nodes.forEach((node) => {
        const pos = positions[node.id];
        const classes = ['graph-node'];
        if (changed.has(node.id)) classes.push('diff-changed');
        else if (onlyA.has(node.id)) classes.push('diff-a');
        else if (onlyB.has(node.id)) classes.push('diff-b');
        html += '<g class="' + classes.join(' ') + '" transform="translate(' + pos.x + ',' + pos.y + ')"><rect width="120" height="48" rx="8"></rect>';
        html += '<text x="8" y="18" font-weight="700">' + escapeHTML(node.id) + '</text></g>';
      });
      html += '</svg>';
      container.innerHTML = html;
    }
    async function loadThread() {
      if (!state.selectedRun) {
        $('threadCanvas').innerHTML = '<div class="empty">' + escapeHTML(t('empty.selectRun')) + '</div>';
        return;
      }
      const res = await fetch('api/runs/' + encodeURIComponent(state.selectedRun) + '/thread');
      const body = await res.json();
      if (!res.ok) { $('threadCanvas').innerHTML = '<div class="empty">' + escapeHTML(formatApiError(body, 'empty.threadUnavailable')) + '</div>'; return; }
      state.threadRuns = body.runs || [];
      renderThread();
    }
    function renderThread() {
      const canvas = $('threadCanvas');
      canvas.innerHTML = state.threadRuns.length ? state.threadRuns.map((run) =>
        '<div class="thread-item ' + (run.run_id === state.selectedRun ? 'active' : '') + '" data-run="' + escapeHTML(run.run_id) + '">' +
          '<div class="title">' + escapeHTML(run.run_id) + ' · ' + escapeHTML(run.status) + '</div>' +
          '<div class="meta"><span>' + escapeHTML(t('thread.thread')) + ' ' + escapeHTML(run.thread_id) + '</span>' +
          (run.parent_run_id ? '<span>' + escapeHTML(t('thread.forkOf')) + ' ' + escapeHTML(run.parent_run_id) + '</span>' : '<span>' + escapeHTML(t('thread.root')) + '</span>') +
          '</div></div>').join('') : '<div class="empty">' + escapeHTML(t('empty.noThreadRuns')) + '</div>';
      canvas.querySelectorAll('.thread-item').forEach((node) => node.onclick = () => selectRun(node.dataset.run));
    }
    async function forkCurrentRun() {
      if (!state.selectedRun) return;
      const res = await fetch('api/runs/' + encodeURIComponent(state.selectedRun) + '/fork', {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}',
      });
      const body = await res.json();
      if (!res.ok) { alert(formatApiError(body, 'alert.forkFailed')); return; }
      await loadRuns();
      await selectRun(body.run_id);
      setView('thread');
    }
    function updateHitlBar() {
      const bar = $('hitlBar');
      if (!bar) return;
      const pending = state.steps && state.steps.pending_hitl;
      const paused = state.steps && state.steps.status === 'paused';
      const show = state.hitlEnabled && paused && pending;
      bar.hidden = !show;
      if (!show) return;
      const nodeID = pending.node_id || (state.steps && state.steps.current_node_id) || '?';
      const kind = pending.interrupt ? t('hitl.interrupt') : t('hitl.humanGate');
      $('hitlBarLabel').textContent = t('hitl.pending', { node: nodeID, kind });
    }
    async function resumeRunHITL(decision) {
      if (!state.selectedRun) return;
      const res = await fetch('api/runs/' + encodeURIComponent(state.selectedRun) + '/hitl/resume', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ decision }),
      });
      const body = await res.json();
      if (!res.ok) { alert(formatApiError(body, 'alert.hitlFailed')); return; }
      await loadSteps(state.selectedRun);
      await loadRuns();
      await selectRun(state.selectedRun);
    }
    function updateTimeTravelBar() {
      const meta = state.nodeMeta[state.selectedNode] || {};
      const hint = meta.resume_hint || (meta.resumable === false ? t('timeTravel.notResumable') : '');
      $('selectedNodeLabel').textContent = state.selectedNode
        ? t('timeTravel.nodeLabel', { node: state.selectedNode, hint: hint ? ' — ' + hint : '' })
        : t('timeTravel.noNode');
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
        alert(formatApiError(body, 'alert.resumeFailed'));
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
        alert(formatApiError(body, 'alert.resumeFailed'));
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
          '<div class="run-main"><div class="run-id">' + escapeHTML(run.run_id) + '</div><span class="badge ' + escapeHTML(run.status) + '">' + escapeHTML(statusLabel(run.status)) + '</span></div>' +
          '<div class="meta"><span>' + escapeHTML(run.scenario_name || t('run.scenarioFallback')) + '</span><span>' + escapeHTML(t('run.events', { n: run.event_count })) + '</span><span>' + fmtTime(run.last_seen_at) + '</span></div>' +
        '</div>').join('') : '<div class="empty">' + escapeHTML(t('empty.noSessions')) + '</div>';
      document.querySelectorAll('.run').forEach((node) => node.onclick = () => selectRun(node.dataset.run));
    }
    function renderEvents() {
      $('selectedRun').textContent = state.selectedRun || t('empty.noRun');
      $('statEvents').textContent = state.events.length;
      $('statTools').textContent = state.events.filter((record) => record.event.type.includes('ToolCalled')).length;
      $('statLLM').textContent = state.events.filter((record) => record.event.type.includes('LLMCalled')).length;
      $('events').innerHTML = state.events.length ? state.events.map((record) =>
        '<div class="event ' + eventKind(record.event.type) + ' ' + (state.selectedEvent && state.selectedEvent.id === record.id ? 'active' : '') + '" data-id="' + record.id + '">' +
          '<div class="event-main"><div class="event-type">' + escapeHTML(record.event.type) + '</div><span class="badge">#' + record.sequence + '</span></div>' +
          '<div class="meta"><span>' + fmtTime(record.event.timestamp) + '</span><span>' + escapeHTML(record.event.trace_id || 'trace -') + '</span><span>' + escapeHTML(record.event.span_id || 'span -') + '</span></div>' +
        '</div>').join('') : '<div class="empty">' + escapeHTML(state.selectedRun ? t('empty.noEvents') : t('empty.selectSession')) + '</div>';
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
      $('detailType').textContent = record ? record.event.type : (state.selectedCheckpoint ? t('detail.checkpoint') : t('detail.run'));
      if (record) {
        $('details').innerHTML =
          '<div class="kv"><span>' + escapeHTML(t('detail.run')) + '</span><span>' + escapeHTML(record.event.run_id) + '</span></div>' +
          '<div class="kv"><span>' + escapeHTML(t('detail.scenario')) + '</span><span>' + escapeHTML(record.event.scenario_name || '-') + '</span></div>' +
          '<div class="kv"><span>' + escapeHTML(t('detail.sequence')) + '</span><span>' + record.sequence + '</span></div>' +
          '<div class="kv"><span>' + escapeHTML(t('detail.occurred')) + '</span><span>' + escapeHTML(record.event.timestamp) + '</span></div>' +
          '<div class="kv"><span>' + escapeHTML(t('detail.stored')) + '</span><span>' + escapeHTML(record.created_at) + '</span></div>' +
          '<pre>' + escapeHTML(JSON.stringify(record.event.payload || {}, null, 2)) + '</pre>';
        return;
      }
      let html = '';
      if (state.selectedRun) {
        html += '<div class="kv"><span>' + escapeHTML(t('detail.run')) + '</span><span>' + escapeHTML(state.selectedRun) + '</span></div>';
      }
      if (state.selectedCheckpoint) {
        const cp = state.selectedCheckpoint;
        html += '<div class="kv"><span>' + escapeHTML(t('detail.version')) + '</span><span>v' + cp.version + '</span></div>';
        html += '<div class="kv"><span>' + escapeHTML(t('detail.status')) + '</span><span>' + escapeHTML(statusLabel(cp.status)) + '</span></div>';
        html += '<div class="kv"><span>' + escapeHTML(t('detail.steps')) + '</span><span>' + cp.step_count + '</span></div>';
        html += '<div class="kv"><span>' + escapeHTML(t('detail.recorded')) + '</span><span>' + fmtTime(cp.recorded_at) + '</span></div>';
        if (cp.current_node_id) {
          html += '<div class="kv"><span>' + escapeHTML(t('detail.current')) + '</span><span>' + escapeHTML(cp.current_node_id) + '</span></div>';
        }
      }
      if (state.checkpointEnabled && state.checkpoints && state.checkpoints.checkpoints) {
        const items = state.checkpoints.checkpoints;
        html += '<div class="panel-title" style="margin-top:8px;">' + escapeHTML(t('detail.checkpointHistory')) + '</div>';
        html += items.length ? '<div class="checkpoint-list">' + items.map((cp) =>
          '<div class="checkpoint-item ' + (state.selectedCheckpoint && state.selectedCheckpoint.version === cp.version ? 'active' : '') + '" data-version="' + cp.version + '">' +
            '<div class="title">v' + cp.version + ' · ' + escapeHTML(statusLabel(cp.status)) + '</div>' +
            '<div class="meta"><span>' + escapeHTML(t('detail.stepsCount', { n: cp.step_count })) + '</span><span>' + fmtTime(cp.recorded_at) + '</span></div>' +
          '</div>').join('') + '</div>' : '<div class="empty">' + escapeHTML(t('empty.noCheckpoints')) + '</div>';
      } else if (state.selectedRun) {
        html += '<div class="empty">' + escapeHTML(t('empty.checkpointRequiresHistory')) + '</div>';
      }
      if (!html) {
        html = '<div class="empty">' + escapeHTML(t('empty.selectEventOrCheckpoint')) + '</div>';
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
    $('editorTab').onclick = () => setView('editor');
    $('compareTab').onclick = () => setView('compare');
    $('threadTab').onclick = () => setView('thread');
    $('editorTargetSelect').onchange = () => switchEditorTarget();
    $('addSubgraphButton').onclick = () => addEditorSubgraph();
    $('saveNodePropsButton').onclick = () => applyEditorNodeProps();
    $('saveEdgePropsButton').onclick = () => applyEditorEdgeProps();
    $('addNodeButton').onclick = () => addEditorNode();
    $('deleteNodeButton').onclick = () => deleteEditorNode();
    $('deleteEdgeButton').onclick = () => deleteEditorEdge();
    $('connectModeButton').onclick = () => {
      state.editorConnectFrom = state.selectedNode || '';
      updateConnectButton();
    };
    $('validateGraphButton').onclick = () => validateEditorGraph();
    $('yamlGraphButton').onclick = () => yamlEditorGraph();
    $('importYamlButton').onclick = () => $('importYamlFile').click();
    $('importYamlFile').onchange = async (event) => {
      const file = event.target.files && event.target.files[0];
      if (!file) return;
      try {
        await importEditorYaml(await file.text());
      } finally {
        event.target.value = '';
      }
    };
    $('previewSaveButton').onclick = () => previewSaveEditorGraph();
    $('saveGraphButton').onclick = () => saveEditorGraph();
    $('revertGraphButton').onclick = () => revertEditorGraph();
    $('codegenGraphButton').onclick = () => codegenEditorGraph();
    $('runEditorGraphButton').onclick = () => runEditorGraph();
    $('undoEditorButton').onclick = () => undoEditor();
    $('redoEditorButton').onclick = () => redoEditor();
    document.querySelectorAll('#editorPalette .node-chip').forEach((chip) => {
      chip.onclick = () => {
        $('editorNodeKind').value = chip.dataset.kind;
        addEditorNode(chip.dataset.kind);
      };
    });
    $('compareRunsButton').onclick = () => compareRuns();
    $('forkRunButton').onclick = () => forkCurrentRun();
    $('refreshThreadButton').onclick = () => loadThread();
    $('resumeStepButton').onclick = () => resumeFromStep();
    $('resumeCheckpointButton').onclick = () => resumeFromCheckpoint();
    $('hitlApproveButton').onclick = () => resumeRunHITL('approve');
    $('hitlRejectButton').onclick = () => resumeRunHITL('reject');
    $('liveButton').onclick = () => {
      state.live = !state.live;
      updateLiveButton();
      if (state.live) openStream(); else closeStream();
    };
    $('langSelect').value = locale;
    $('langSelect').onchange = () => setLocale($('langSelect').value);
    document.addEventListener('keydown', (event) => {
      if (state.view !== 'editor') return;
      const tag = (event.target && event.target.tagName || '').toLowerCase();
      if (tag === 'input' || tag === 'textarea' || tag === 'select' || event.target.isContentEditable) return;
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 's') {
        event.preventDefault();
        saveEditorGraph();
      } else if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'z' && !event.shiftKey) {
        event.preventDefault();
        undoEditor();
      } else if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'z' && event.shiftKey) {
        event.preventDefault();
        redoEditor();
      } else if (event.key === 'Delete' || event.key === 'Backspace') {
        if (state.selectedEdge) deleteEditorEdge();
        else if (state.selectedNode || (state.selectedNodes || []).length) deleteEditorNode();
      }
    });
    applyStaticI18n();
    loadGraph().then(() => loadRuns());
    setInterval(() => { if (state.live) loadRuns(); }, 3000);
  </script>
</body>
</html>`

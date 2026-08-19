(() => {
  const $ = (selector, root = document) => root.querySelector(selector);
  const $$ = (selector, root = document) => [...root.querySelectorAll(selector)];
  const esc = (value) => String(value ?? '').replace(/[&<>"']/g, (char) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char]));
  const nav = $('#navigator');
  const scrim = $('#scrim');
  const workspace = $('#workspace');
  const search = $('#session-search');
  const statusPanel = $('#status-panel');
  const artifactPanel = $('#artifact-panel');
  const pane = $('#pane-wrap');
  const composer = $('#composer');
  const sessionMenu = $('#session-menu');
  const actionsMenu = $('#actions-menu');
  const artifactMenu = $('#artifact-actions-menu');
  const outputReader = $('#output-reader');
  const dialogReturns = new WeakMap();
  let activeBranch = 'jf-ui-refresh';
  let split = 'terminal';
  let selectedArtifactID = null;
  let artifactSort = 'newest';
  let jsxMode = 'preview';
  let listCollapsed = false;
  let navReturn = null;
  let navPreviousDestination = 'terminal';
  let menuReturn = null;
  let panelReturn = null;
  let composerReturn = null;
  let outputReturn = null;
  let artifactFullReturn = null;
  let nextCreated = 1;
  let outputBlobURL = null;

  const colors = ['#a855f7','#22c55e','#f97316','#06b6d4','#ef4444','#3b82f6','#eab308','#ec4899'];
  const svgPreview = (label, color = '#0ea5e9') => 'data:image/svg+xml,' + encodeURIComponent(`<svg xmlns="http://www.w3.org/2000/svg" width="900" height="520"><rect width="900" height="520" fill="#07101c"/><rect x="55" y="55" width="790" height="410" rx="24" fill="#0d1b2b" stroke="${color}" stroke-width="3"/><circle cx="112" cy="110" r="18" fill="#4ade80"/><path d="M95 372l160-142 126 92 104-83 222 133" fill="none" stroke="${color}" stroke-width="18"/><text x="95" y="160" fill="#eef5ff" font-family="monospace" font-size="34">${label}</text></svg>`);
  const artifact = (id, title, type, age, content = '', options = {}) => ({
    id, title, type, created: Date.now() - age * 3600000, content,
    path: options.path || `.artifacts/${title}`,
    summary: options.summary || '',
    tags: options.tags || [],
    retention: options.retention || 'keep',
    ...options
  });
  const jsxSample = `export default function FleetSummary() {\n  return (\n    <section className="rounded-xl bg-slate-900 p-8">\n      <h2>Fleet is ready</h2>\n      <p>25 complete session fixtures · compact navigation</p>\n    </section>\n  )\n}`;
  const fixture = (title, project, branch, status, target, index, options = {}) => ({
    title, project, branch, status, target,
    statusTone: status === 'active' ? 'green' : status === 'attention' || status === 'dirty' ? 'amber' : status === 'repair' ? 'red' : 'muted',
    tmux: status === 'active' || status === 'dirty' ? 'active' : status === 'repair' ? 'unknown' : 'stopped',
    dirty: status === 'dirty' ? true : status === 'repair' ? null : false,
    reason: options.reason || ({active:'tmux is running or recent activity was observed',attention:'flagged for explicit operator review',dirty:'worktree has uncommitted, untracked, or unpushed work',repair:'worktree is missing or inaccessible; git state is unknown',idle:'no running tmux session or recent activity'}[status]),
    routes: options.routes || (index % 3 === 0 ? [['ui',`${branch}-ui.localhost`],['api',`${branch}-api.localhost`]] : index % 2 === 0 ? [['ui',`${branch}-ui.localhost`]] : []),
    artifacts: options.artifacts || (index % 4 === 0 ? [artifact(`${branch}-shot`,`${title.toLowerCase().replaceAll(' ','-')}.png`,'image',index + 1,svgPreview(title)),artifact(`${branch}-notes`,'review-notes.md','markdown',index + 4,`# ${title}\n\nSession-scoped review notes for ${branch}.\n\n- Target: ${target}\n- Status: ${status}\n- Fixtures are descriptive, not live telemetry.`)] : index % 3 === 0 ? [artifact(`${branch}-log`,'verification.log','text',index + 2,`PASS ${branch}\nNo console errors\nViewport containment checked`)] : []),
    gatepost: target === 'gatepost' ? `https://${branch}-logs.localhost` : null,
    color: colors[index % colors.length]
  });

  const fleet = [
    fixture('Operator console refresh','devx','jf-ui-refresh','active','host',0,{reason:'tmux is running; worktree scan is clean',routes:[['ui','operator-console-ui.localhost'],['api','operator-console-api.localhost'],['docs','operator-console-docs.localhost']],artifacts:[artifact('console-shot','operator-console.png','image',1,svgPreview('Operator Console'),{summary:'Desktop console proof',tags:['ui','proof']}),artifact('review','ui-review.md','markdown',3,'# UI review\n\nCompact rows preserve status and personal color.\n\nOutput uses the full viewport.\n\nArtifact tools match the production surface.',{path:'.artifacts/reports/ui-review.md',summary:'Review handoff',tags:['review']}),artifact('component','FleetSummary.jsx','jsx',5,jsxSample,{path:'.artifacts/components/FleetSummary.jsx',retention:'30 days'}),artifact('verify','verification.txt','text',8,'PASS 1440×1000\nPASS 1024×768\nPASS 768×1024\nPASS 390×844\nPASS 320×700'),artifact('walkthrough','walkthrough.mp4','video',9,'',{path:'.artifacts/media/walkthrough.mp4'}),artifact('report','report.html','html',10,'<main style="font:16px system-ui;padding:24px"><h1>Operator report</h1><p>Static HTML artifact preview.</p></main>',{path:'.artifacts/reports/report.html'}),artifact('brief','brief.pdf','pdf',11,'',{path:'.artifacts/reports/brief.pdf'}),artifact('bundle','fixtures.zip','binary',12,'',{path:'.artifacts/archive/fixtures.zip'})]}),
    fixture('Gatepost proxy consolidation','devx','jf-gatepost-proxy','attention','gatepost',1,{routes:[['api','gatepost-proxy-api.localhost'],['logs','gatepost-proxy-logs.localhost']],artifacts:[artifact('proxy-review','proxy-review.md','markdown',4,'# Proxy review\n\nOperator attention is required before route cleanup.')]}),
    fixture('Artifact search indexing','devx','jf-artifact-search','dirty','host',2),
    fixture('Terminal reconnect handling','devx','jf-reconnect','idle','docker',3),
    fixture('Recent activity model','devx','jf-recent-activity','active','host',4),
    fixture('Onboarding prompts','devx','jf-onboarding','idle','host',5),
    fixture('Update services workflow','devx','jf-update-services','active','docker',6),
    fixture('Session cleanup audit','devx','jf-cleanup-audit','attention','host',7),
    fixture('Unread semantics audit','media-memory','jf-unread-audit','active','host',8,{artifacts:[artifact('audit-notes','audit-notes.md','markdown',2,'# Unread audit\n\nNew artifacts remain visible without turning green semantic state into selection.'),artifact('states','states.png','image',5,svgPreview('Unread States','#38bdf8'))]}),
    fixture('Privacy-safe MCP logging','media-memory','jf-mcp-audit','repair','host',9,{routes:[],artifacts:[]}),
    fixture('Dark mode tokens','media-memory','jf-dark-mode','attention','host',10),
    fixture('Fixing ingest retry','media-memory','jf-mm-fix-ingest','attention','docker',11),
    fixture('Reaction event stream','media-memory','jf-reactions','active','host',12),
    fixture('Search relevance pass','media-memory','jf-search-relevance','idle','host',13),
    fixture('Dashboard response states','nibit','jf-dashboard-states','active','docker',14,{artifacts:[artifact('response-grid','response-grid.png','image',2,svgPreview('Response Grid','#4ade80')),artifact('edge-cases','edge-cases.md','markdown',7,'# Edge cases\n\nEmpty, loading, reconnecting, and error states.')]}),
    fixture('Cross-device review','nibit','jf-review-cross-device','attention','host',15),
    fixture('Build pipeline notes','nibit','jf-build-pipeline','active','docker',16),
    fixture('Dynamic route cards','nibit','jf-dynamic-routes','idle','host',17),
    fixture('Mobile command dock','nibit','jf-mobile-dock','active','host',18),
    fixture('Approval state copy','nibit','jf-approval-copy','dirty','host',19),
    fixture('Landing page system','gatepost','jf-landing-page','idle','gatepost',20),
    fixture('Metrics console','gatepost','jf-metrics-console','active','gatepost',21),
    fixture('Tunnel health wording','gatepost','jf-tunnel-wording','attention','gatepost',22),
    fixture('Route retention policy','gatepost','jf-route-retention','idle','gatepost',23),
    fixture('Log viewer density','gatepost','jf-log-density','active','gatepost',24)
  ];
  const fixtures = Object.fromEntries(fleet.map((item) => [item.branch,item]));
  const current = () => fixtures[activeBranch];
  const isEditable = (element) => Boolean(element?.closest('input,textarea,select,[contenteditable="true"]'));
  const isPhone = () => innerWidth <= 600;
  const isCompact = () => innerWidth <= 1180;
  const focusables = (root) => $$('button:not([disabled]),a[href],input:not([disabled]),textarea:not([disabled]),select:not([disabled]),[tabindex]:not([tabindex="-1"])', root).filter((element) => !element.hidden && element.offsetParent !== null);
  const toneColor = (tone) => tone === 'green' ? 'var(--green)' : tone === 'amber' ? 'var(--amber)' : tone === 'red' ? 'var(--red)' : 'var(--muted)';
  const statusGlyph = (status) => ({active:'▶',attention:'!',dirty:'±',repair:'⚠',idle:'scan'}[status] || '·');
  const dotClass = (status) => ({active:'live',attention:'attention',dirty:'dirty',repair:'broken'}[status] || '');

  function renderFleet() {
    const priority = {repair:0, attention:1, dirty:2, active:3, idle:4};
    const projects = [...new Set(fleet.map((session) => session.project))].sort((a,b) => a.localeCompare(b));
    $('#fleet-groups').innerHTML = projects.map((project) => {
      const sessions = fleet.filter((session) => session.project === project).sort((a,b) => (priority[a.status] ?? 99) - (priority[b.status] ?? 99) || a.title.localeCompare(b.title));
      return `<section class="project" data-project="${esc(project)}"><button class="project-toggle" aria-expanded="true"><span class="chevron">⌄</span>${esc(project)}<span class="count">${sessions.length}</span></button><div class="sessions">${sessions.map((session) => {
        const artifactCount = session.artifacts.filter((item) => !item.removed).length;
        const detail = `${session.branch} · ${session.reason} · status ${session.status} · ${artifactCount} artifacts`;
        return `<button class="session${session.branch === activeBranch ? ' selected' : ''}" data-title="${esc(session.title)}" data-project="${esc(session.project)}" data-branch="${esc(session.branch)}" data-status="${esc(session.status)}" data-detail="${esc(detail)}" title="${esc(`${session.title} — ${detail}`)}" aria-label="${esc(`${session.title}, ${session.status}, ${session.target} target, ${artifactCount} artifacts`)}"${session.branch === activeBranch ? ' aria-current="true"' : ''}><span class="status-dot ${dotClass(session.status)}" aria-hidden="true"></span><span class="swatch" style="background:${esc(session.color)}" title="Session color identifier" aria-hidden="true"></span><span class="session-main"><span class="session-name">${esc(session.title)}</span><span class="branch">${esc(session.branch)}</span></span><span class="badge">${esc(session.target)}</span><span class="session-state ${esc(session.status)}" title="Status: ${esc(session.status)}" aria-label="Status: ${esc(session.status)}"><span aria-hidden="true">${statusGlyph(session.status)}</span></span>${artifactCount ? `<span class="badge blue" title="${artifactCount} artifacts" aria-label="${artifactCount} artifacts">◆ ${artifactCount}</span>` : '<span class="badge blue" aria-label="0 artifacts">◆ 0</span>'}</button>`;
      }).join('')}</div></section>`;
    }).join('');
    $('#active-count').textContent = fleet.filter((s) => s.status === 'active').length;
    $('#flagged-count').textContent = fleet.filter((s) => s.status === 'attention').length;
    $('#total-count').textContent = fleet.length;
    bindProjectToggles();
    $$('.session').forEach((row) => row.onkeydown = navigateFleet);
  }

  function bindProjectToggles() {
    $$('.project-toggle').forEach((button) => button.onclick = () => {
      const project = button.closest('.project');
      const collapsed = project.classList.toggle('collapsed');
      button.setAttribute('aria-expanded', String(!collapsed));
    });
  }

  function artifactItems(session = current()) { return session.artifacts.filter((item) => !item.removed); }
  function sortedArtifacts() {
    const items = [...artifactItems()];
    if (artifactSort === 'oldest') return items.sort((a,b) => a.created - b.created);
    if (artifactSort === 'title') return items.sort((a,b) => a.title.localeCompare(b.title));
    return items.sort((a,b) => b.created - a.created);
  }
  function artifactKind(item) { return item?.type || 'text'; }

  function renderArtifacts() {
    const items = sortedArtifacts();
    if (!items.some((item) => item.id === selectedArtifactID)) selectedArtifactID = items[0]?.id || null;
    $('#artifact-list').classList.toggle('collapsed', listCollapsed);
    $('#artifact-resizer').hidden = listCollapsed || !items.length;
    $('#artifact-list-toggle').textContent = listCollapsed ? 'Show list' : 'Hide list';
    $('#artifact-sort').value = artifactSort;
    const groups = new Map();
    items.forEach((item) => {
      const relative = item.path.replace(/^\.artifacts\/?/, '');
      const folder = relative.includes('/') ? `.artifacts/${relative.slice(0, relative.lastIndexOf('/'))}` : '.artifacts';
      if (!groups.has(folder)) groups.set(folder, []);
      groups.get(folder).push(item);
    });
    $('#artifact-list').innerHTML = items.length ? [...groups].map(([folder, folderItems]) => `<div class="artifact-folder">${esc(folder)}</div>${folderItems.map((item) => `<button class="artifact-list-item${item.id === selectedArtifactID ? ' selected' : ''}" data-artifact-id="${esc(item.id)}" aria-current="${item.id === selectedArtifactID}"><b>${esc(item.title)}${item.archived ? ' · archived' : ''}</b><span>${esc(item.type)} · ${Math.max(1,Math.round((Date.now() - item.created)/3600000))}h ago · ${esc(item.retention)}</span></button>`).join('')}`).join('') : '<div class="detail"><p class="dim">No artifacts — upload files, paste, drop, or create an artifact.</p></div>';
    const selected = items.find((item) => item.id === selectedArtifactID);
    if (!selected) {
      $('#artifact-preview-head').innerHTML = '<div class="preview-title"><b>No artifact selected</b><small>Create, upload, paste, or drop to begin</small></div>';
      $('#artifact-preview').innerHTML = '<p class="dim">Preview appears here.</p>';
      return;
    }
    const toggle = selected.type === 'jsx' ? `<button class="artifact-tool" data-artifact-command="jsx">${jsxMode === 'preview' ? 'Code' : 'Preview'}</button>` : '';
    const metadata = [selected.path, selected.summary, selected.tags.length ? `#${selected.tags.join(' #')}` : '', selected.retention].filter(Boolean).join(' · ');
    $('#artifact-preview-head').innerHTML = `<div class="preview-title"><b>${esc(selected.title)}</b><small>${esc(metadata)}</small></div>${toggle}<button class="artifact-tool" data-artifact-command="insert">Insert</button><button class="artifact-tool" data-artifact-command="edit">Edit</button><button class="artifact-tool" data-artifact-command="archive">Archive</button><button class="artifact-tool" data-artifact-command="remove" style="color:var(--red)">Remove</button>`;
    if (selected.type === 'image') $('#artifact-preview').innerHTML = `<img src="${selected.content}" alt="Preview of ${esc(selected.title)}" style="display:block;max-width:100%;max-height:100%;margin:auto;object-fit:contain">`;
    else if (selected.type === 'jsx' && jsxMode === 'preview') $('#artifact-preview').innerHTML = '<div class="jsx-card"><h2>Fleet is ready</h2><p>25 complete session fixtures use compact one-line navigation. The JSX preview/code toggle is functional.</p><button class="primary">Review sessions</button></div>';
    else if (selected.type === 'video') $('#artifact-preview').innerHTML = `<video controls aria-label="Video preview for ${esc(selected.title)}"></video><p class="dim">Video player state · static fixture has no encoded media payload.</p>`;
    else if (selected.type === 'html') $('#artifact-preview').innerHTML = `<iframe title="HTML preview for ${esc(selected.title)}" sandbox srcdoc="${esc(selected.content)}"></iframe>`;
    else if (selected.type === 'pdf') $('#artifact-preview').innerHTML = `<iframe title="PDF preview for ${esc(selected.title)}" srcdoc="${esc('<p style=\"font:16px system-ui;padding:24px\">PDF/iframe preview state for '+selected.title+'</p>')}"></iframe>`;
    else if (selected.type === 'binary') $('#artifact-preview').innerHTML = `<div class="detail"><h2>No inline preview</h2><p>${esc(selected.title)} cannot be previewed here. Download or open it with a compatible application.</p></div>`;
    else $('#artifact-preview').innerHTML = `<pre>${esc(selected.content || `${selected.title}\n\nStatic preview content for ${current().branch}.`)}</pre>`;
    const full = artifactPanel.classList.contains('fullscreen');
    $('#artifact-fullscreen').textContent = full ? 'Exit Full' : 'Full Screen';
    const menuButtons = $$('[data-artifact-command]', artifactMenu);
    menuButtons.forEach((button) => {
      if (button.dataset.artifactCommand === 'sort') button.textContent = `Sort: ${artifactSort}`;
      if (button.dataset.artifactCommand === 'list') button.textContent = listCollapsed ? 'Show artifact list' : 'Hide artifact list';
      if (button.dataset.artifactCommand === 'fullscreen') button.textContent = full ? 'Exit Full Screen' : 'Full Screen preview';
    });
  }

  function renderSession() {
    const session = current();
    $('#session-title').textContent = session.title;
    $('#session-title').title = session.title;
    $('#project-label').textContent = session.project;
    $('#branch-label').textContent = session.branch;
    $('#status-label').textContent = session.status;
    $('#status-label').style.color = toneColor(session.statusTone);
    $('#mobile-text').placeholder = `Message ${session.title}…`;
    $('.composer-head span').textContent = `Compose → ${session.title}`;
    const dirty = session.dirty === null ? '<b>unknown</b> git state' : `<b>${session.dirty ? 'dirty' : 'clean'}</b> worktree`;
    $('#session-facts').innerHTML = `<div class="fact"><span class="${session.tmux === 'active' ? 'good' : ''}">●</span> <b>tmux ${esc(session.tmux)}</b></div><div class="fact"><b>${esc(session.target)}</b> target</div><div class="fact">${dirty}</div><div class="fact"><button class="fact-button" data-open-status>${session.routes.length} ${session.routes.length === 1 ? 'route' : 'routes'}</button></div><div class="fact"><button class="fact-button" data-open-artifacts>${artifactItems().length} ${artifactItems().length === 1 ? 'artifact' : 'artifacts'}</button></div>`;
    const routes = session.routes.length ? session.routes.map(([name,address]) => `<a class="route-link" href="https://${esc(address)}" target="_blank" rel="noreferrer">↗ ${esc(name)} · ${esc(address)}</a>`).join('') : '<p class="dim">No route addresses in this fixture.</p>';
    const gatepost = session.gatepost ? `<p>Enabled for this session.</p><a class="route-link" href="${esc(session.gatepost)}" target="_blank" rel="noreferrer">↗ Open external Gatepost logs</a>` : '<p>Not enabled for this session.</p>';
    $('#status-content').innerHTML = `<div class="detail"><h2>Status</h2><p><span style="color:${toneColor(session.statusTone)}">● ${esc(session.status)}</span> — ${esc(session.reason)}.</p><button class="small-btn" data-status-legend>Status legend</button></div><div class="detail"><h2>Branch and target</h2><p><b>${esc(session.branch)}</b></p><p>${esc(session.target)} target</p></div><div class="detail"><h2>Routes · addresses only</h2>${routes}<p class="dim">No health claim is inferred from route presence.</p></div><div class="detail"><h2>Gatepost</h2>${gatepost}</div><div class="detail"><h2>Artifacts</h2><p>${artifactItems().length} session artifacts</p><button class="small-btn" data-open-artifacts>Open artifact pane</button></div>`;
    const lines = Array.from({length:64},(_,i) => `<div class="term-row"><span class="dim">${String(i+1).padStart(2,'0')}</span> ${i % 4 === 0 ? '<span class="green">PASS</span>' : '<span class="cyan">INFO</span>'} ${esc(session.branch)} ${i % 3 === 0 ? 'verified session fixture and viewport containment' : 'operator output remains available for review'}</div>`).join('');
    $('#terminal-content').innerHTML = `<div class="term-row dim">Session fixture loaded for ${esc(session.project)}</div><div class="term-row"><span class="cyan">${esc(session.project)}</span> <b>${esc(session.branch)}</b> <span class="cyan">git:(${esc(session.branch)})</span>${session.dirty ? ' <span class="amber">±</span>' : ''}</div><div class="term-row command">❯ devx status --session ${esc(session.branch)}</div><div class="term-row"><span class="dim">STATUS</span>  <span style="color:${toneColor(session.statusTone)}">${esc(session.status)}</span></div><div class="term-row"><span class="dim">TARGET</span>  ${esc(session.target)}</div><div class="term-row"><span class="dim">TMUX</span>    ${esc(session.tmux)}</div>${lines}<div class="term-row command">❯ <span class="cursor"></span></div>`;
    renderArtifacts();
    renderQuick();
  }

  function renderQuick() {
    $('#quick-dialog .switch-list').innerHTML = fleet.map((session) => `<button class="switch-item${session.branch === activeBranch ? ' selected' : ''}" data-branch="${esc(session.branch)}"${session.branch === activeBranch ? ' aria-current="true"' : ''}><b>${esc(session.title)}</b><span>${esc(session.project)} · ${esc(session.branch)}${session.status === 'attention' ? ' · flagged' : ''}</span></button>`).join('');
  }

  function selectSession(branch, {closeNavigator = true, feedback = false} = {}) {
    if (!fixtures[branch]) return false;
    activeBranch = branch;
    selectedArtifactID = null;
    $$('.session').forEach((row) => {
      const selected = row.dataset.branch === branch;
      row.classList.toggle('selected', selected);
      selected ? row.setAttribute('aria-current','true') : row.removeAttribute('aria-current');
    });
    renderSession();
    if (isPhone()) { closeStatus(false); closeArtifacts(false); setMobileActive('terminal'); }
    if (closeNavigator && innerWidth < 1024 && nav.classList.contains('open')) setNav(false, {restore:false,destination:'terminal'});
    if (feedback) toast('Session switched', `${current().title} is now selected.`);
    return true;
  }

  function setMobileActive(name) {
    $$('.mobile-tab').forEach((button) => {
      const active = button.id === `mobile-${name}`;
      button.classList.toggle('active', active);
      active ? button.setAttribute('aria-current','page') : button.removeAttribute('aria-current');
    });
  }
  const activeMobileDestination = () => $('.mobile-tab.active')?.id.replace('mobile-','') || 'terminal';
  function syncNavigatorMode() {
    if (innerWidth < 1024) {
      const open = nav.classList.contains('open');
      nav.inert = !open; nav.setAttribute('aria-hidden', String(!open));
      if (open) { nav.setAttribute('role','dialog'); nav.setAttribute('aria-modal','true'); }
      else { nav.removeAttribute('role'); nav.removeAttribute('aria-modal'); }
    } else {
      nav.classList.remove('open'); scrim.classList.remove('open'); nav.inert = false; nav.removeAttribute('aria-hidden'); nav.removeAttribute('role'); nav.removeAttribute('aria-modal'); workspace.inert = false; $('.mobile-bottom').inert = false; $('#open-nav').setAttribute('aria-expanded','false');
    }
  }
  function setNav(open, {restore = true, destination} = {}) {
    if (open) { navPreviousDestination = activeMobileDestination(); navReturn = document.activeElement; nav.classList.add('open'); scrim.classList.add('open'); workspace.inert = true; $('.mobile-bottom').inert = true; setMobileActive('sessions'); }
    else { nav.classList.remove('open'); scrim.classList.remove('open'); workspace.inert = false; $('.mobile-bottom').inert = false; setMobileActive(destination || navPreviousDestination); }
    $('#open-nav').setAttribute('aria-expanded', String(open)); syncNavigatorMode();
    if (open) setTimeout(() => search.focus(),0); else if (restore) setTimeout(() => navReturn?.focus(),0);
  }

  function syncPanelIsolation() {
    const statusOpen = statusPanel.classList.contains('open');
    const artifactOpen = artifactPanel.classList.contains('open');
    workspace.classList.toggle('panel-destination', isPhone() && (statusOpen || artifactOpen));
    if (artifactPanel.classList.contains('fullscreen')) { setArtifactIsolation(true); return; }
    if (!isPhone()) { ['.topbar','.facts','.windowbar','.mobile-composer','.terminal-pane','#composer','#navigator','.mobile-bottom','#status-panel'].forEach((selector) => { const element = $(selector); if (element) element.inert = false; }); return; }
    $('.topbar').inert = statusOpen || artifactOpen; $('.facts').inert = statusOpen || artifactOpen; $('.windowbar').inert = statusOpen || artifactOpen; $('.mobile-composer').inert = statusOpen || artifactOpen; $('#stage').inert = statusOpen; $('.terminal-pane').inert = artifactOpen; composer.inert = statusOpen || artifactOpen;
  }
  function openStatus({focus = isPhone(), opener = document.activeElement} = {}) { panelReturn = opener; if (isCompact()) closeArtifacts(false); statusPanel.classList.add('open'); $('#status-toggle').setAttribute('aria-expanded','true'); if (isPhone()) setMobileActive('status'); syncPanelIsolation(); if (focus) setTimeout(() => $('#status-heading').focus(),0); }
  function closeStatus(restore = false) { const wasOpen = statusPanel.classList.contains('open'); statusPanel.classList.remove('open'); $('#status-toggle').setAttribute('aria-expanded','false'); syncPanelIsolation(); if (restore && wasOpen) setTimeout(() => panelReturn?.focus(),0); }
  function applySplit(mode) {
    split = mode; pane.className = 'pane-wrap'; artifactPanel.classList.toggle('open', mode !== 'terminal'); if (mode === 'horizontal') pane.classList.add('horizontal'); if (mode === 'artifacts') pane.classList.add('artifacts-only'); $('#split-label').textContent = mode; $$('[data-action="artifacts"]').forEach((button) => button.classList.toggle('active', mode !== 'terminal')); $('#actions-menu [data-action="split"]').textContent = `Split — current mode: ${mode}`; syncPanelIsolation();
  }
  function openArtifacts({focus = isPhone(), opener = document.activeElement} = {}) { panelReturn = opener; if (isCompact()) closeStatus(false); applySplit(split === 'terminal' ? 'vertical' : split); if (isPhone()) setMobileActive('artifacts'); renderArtifacts(); if (focus) setTimeout(() => $('#artifact-heading').focus(),0); }
  function closeArtifacts(restore = false) { const wasOpen = artifactPanel.classList.contains('open'); if (artifactPanel.classList.contains('fullscreen')) toggleArtifactFull(false); applySplit('terminal'); if (restore && wasOpen) setTimeout(() => panelReturn?.focus(),0); }
  function cycleSplit() { const modes = ['terminal','vertical','horizontal','artifacts']; closeStatus(false); applySplit(modes[(modes.indexOf(split)+1)%modes.length]); }
  function closePanelsToTerminal(restore = false) { closeStatus(false); closeArtifacts(false); setMobileActive('terminal'); syncPanelIsolation(); if (restore) setTimeout(() => $('#mobile-terminal').focus(),0); }

  function setArtifactIsolation(full) {
    $('.shell').inert = full;
    ['#navigator','.topbar','.facts','.windowbar','.terminal-pane','#composer','#status-panel','.mobile-composer','.mobile-bottom'].forEach((selector) => { const element = $(selector); if (element) element.inert = full; });
  }
  function toggleArtifactFull(force, returnTarget = document.activeElement) {
    const full = force ?? !artifactPanel.classList.contains('fullscreen');
    if (full) {
      artifactFullReturn = visibleOwnerTarget(returnTarget);
      document.body.appendChild(artifactPanel);
      artifactPanel.classList.add('fullscreen');
      artifactPanel.setAttribute('role','dialog');
      artifactPanel.setAttribute('aria-modal','true');
      listCollapsed = true;
      setArtifactIsolation(true);
    } else {
      artifactPanel.classList.remove('fullscreen');
      artifactPanel.removeAttribute('role');
      artifactPanel.removeAttribute('aria-modal');
      pane.appendChild(artifactPanel);
      setArtifactIsolation(false);
      syncPanelIsolation();
    }
    renderArtifacts();
    if (full) setTimeout(() => $('#artifact-heading').focus(),0); else setTimeout(() => artifactFullReturn?.focus(),0);
  }
  function artifactCommand(command, owner = document.activeElement) {
    const returnTarget = visibleOwnerTarget(owner);
    hideMenus();
    const selected = artifactItems().find((item) => item.id === selectedArtifactID);
    if (command === 'list') { listCollapsed = !listCollapsed; renderArtifacts(); }
    if (command === 'fullscreen') toggleArtifactFull(undefined, returnTarget);
    if (command === 'upload') $('#artifact-upload').click();
    if (command === 'refresh') { renderArtifacts(); toast('Artifacts refreshed', `${artifactItems().length} session-scoped artifacts loaded.`); }
    if (command === 'close') closeArtifacts(true);
    if (command === 'sort') { artifactSort = artifactSort === 'newest' ? 'oldest' : artifactSort === 'oldest' ? 'title' : 'newest'; renderArtifacts(); }
    if (command === 'jsx') { jsxMode = jsxMode === 'preview' ? 'code' : 'preview'; renderArtifacts(); }
    if (command === 'insert' && selected) insertReference(selected.path, owner);
    if (command === 'edit' && selected) generic('Edit artifact', `<label class="field">Title<input id="edit-artifact-title" value="${esc(selected.title)}"></label><label class="field">Summary<input id="edit-artifact-summary" value="${esc(selected.summary)}"></label><label class="field">Tags<input id="edit-artifact-tags" value="${esc(selected.tags.join(', '))}" placeholder="ui, review"></label><label class="field">Retention<select id="edit-artifact-retention"><option value="keep">Keep</option><option value="7 days">7 days</option><option value="30 days">30 days</option></select></label><button class="primary" id="save-artifact">Save changes</button>`, (body) => { $('#edit-artifact-retention',body).value = selected.retention; $('#save-artifact',body).onclick = () => { const title = $('#edit-artifact-title',body).value.trim(); if (!title) return; selected.title = title; selected.summary = $('#edit-artifact-summary',body).value.trim(); selected.tags = $('#edit-artifact-tags',body).value.split(',').map((tag) => tag.trim()).filter(Boolean); selected.retention = $('#edit-artifact-retention',body).value; $('#generic-dialog').close(); renderArtifacts(); renderFleet(); toast('Artifact updated', title); }; }, owner);
    if (command === 'archive' && selected) { selected.archived = true; renderArtifacts(); toast('Artifact archived', selected.title); }
    if (command === 'remove' && selected) generic('Remove artifact?', `<p>Remove <b>${esc(selected.title)}</b> from this session? This destructive action requires confirmation.</p><button class="danger-btn" id="confirm-remove-artifact">Confirm remove</button>`, (body) => { $('#confirm-remove-artifact',body).onclick = () => { selected.removed = true; selectedArtifactID = null; $('#generic-dialog').close(); renderSession(); renderFleet(); toast('Artifact removed', `${selected.title} was removed from this fixture.`); }; }, owner);
  }

  function updateComposerButtons(textarea, send, paste) { const hasText = textarea.value.trim().length > 0; send.disabled = !hasText; paste.disabled = !hasText; }
  function toggleCompose(force) { const open = force ?? !composer.classList.contains('open'); if (open) composerReturn = document.activeElement; composer.classList.toggle('open',open); if (open) setTimeout(() => $('textarea',composer).focus(),0); else if (force === false && composerReturn && document.activeElement?.closest('#composer')) setTimeout(() => composerReturn.focus(),0); }
  function visibleOwnerTarget(target = document.activeElement) { if (target?.closest?.('#session-menu')) return $('#session-options'); if (target?.closest?.('#actions-menu')) return innerWidth < 1024 ? $('#mobile-actions') : $('#more-actions'); if (target?.closest?.('#artifact-actions-menu')) return $('#artifact-menu-trigger'); return target; }
  function openDialog(dialog, returnTarget = document.activeElement) { dialogReturns.set(dialog,visibleOwnerTarget(returnTarget)); if (!dialog.open) dialog.showModal(); }
  function generic(title, html, setup, returnTarget = document.activeElement) { $('#generic-title').textContent = title; $('#generic-body').innerHTML = html; openDialog($('#generic-dialog'),returnTarget); setup?.($('#generic-body')); }
  function toast(title,text,{flag=false,duration=4200}={}) { const item=document.createElement('div'); item.className=`toast${flag?' flag':''}`; item.setAttribute('role','status'); item.innerHTML=`<b>${esc(title)}</b><span>${esc(text)}</span>`; $('#toasts').append(item); if(duration)setTimeout(()=>item.remove(),duration); return item; }

  function hideMenus({restore=false}={}) {
    const hadOpen = !sessionMenu.hidden || !actionsMenu.hidden || !artifactMenu.hidden;
    sessionMenu.hidden = true; actionsMenu.hidden = true; artifactMenu.hidden = true;
    $('#session-options').setAttribute('aria-expanded','false'); $('#mobile-actions').setAttribute('aria-expanded','false'); $('#more-actions').setAttribute('aria-expanded','false'); $('#artifact-menu-trigger').setAttribute('aria-expanded','false');
    if (restore && hadOpen) setTimeout(() => menuReturn?.focus(),0);
  }
  function toggleMenu(menu,button) { const opening=menu.hidden; hideMenus(); if(!opening)return; menuReturn=button; menu.hidden=false; button.setAttribute('aria-expanded','true'); setTimeout(()=>$('button:not([disabled])',menu)?.focus(),0); }

  function openOutput(owner) {
    outputReturn = owner;
    const text = $('#terminal-content').innerText;
    $('#output-reader-session').textContent = `${current().project} / ${current().branch} · readable terminal transcript`;
    $('#output-reader-body pre').textContent = text;
    $('#output-reader-body').scrollTop = 0;
    if (outputBlobURL) URL.revokeObjectURL(outputBlobURL);
    outputBlobURL = URL.createObjectURL(new Blob([text], {type:'text/plain;charset=utf-8'}));
    $('#output-open-tab').href = outputBlobURL;
    outputReader.hidden = false;
    $('.shell').inert = true;
    setTimeout(() => $('#output-reader-heading').focus(),0);
  }
  function closeOutput() { if(outputReader.hidden)return; outputReader.hidden=true; $('.shell').inert=false; setTimeout(() => outputReturn?.focus(),0); }
  function openNewArtifact(returnTarget) {
    generic('New artifact','<label class="field">Title<input id="artifact-title" placeholder="review-notes.md"></label><label class="field">Format<select id="artifact-format"><option value="markdown">Markdown</option><option value="text">Text</option><option value="html">HTML</option><option value="jsx">JSX</option></select></label><label class="field">Retention<select id="artifact-retention"><option value="keep">Keep</option><option value="7 days">7 days</option><option value="30 days">30 days</option></select></label><label class="field">Tags<input id="artifact-tags" placeholder="review, ui"></label><label class="field">Artifact text<textarea id="artifact-text" rows="8" placeholder="Paste notes, logs, JSX, or a handoff…"></textarea></label><button class="primary" id="create-artifact" disabled>Create artifact</button>',(body)=>{
      const title=$('#artifact-title',body), input=$('#artifact-text',body), create=$('#create-artifact',body); const validate=()=>{create.disabled=!input.value.trim();}; input.oninput=validate;
      create.onclick=()=>{const format=$('#artifact-format',body).value; const extension={markdown:'md',text:'txt',html:'html',jsx:'jsx'}[format]; const name=title.value.trim()||`note-${artifactItems().length+1}.${extension}`; const item=artifact(`${current().branch}-${Date.now()}`,name,format,0,input.value,{retention:$('#artifact-retention',body).value,tags:$('#artifact-tags',body).value.split(',').map((tag)=>tag.trim()).filter(Boolean),summary:'Created in Operator Console'}); current().artifacts.push(item); selectedArtifactID=item.id; $('#generic-dialog').close(); renderSession(); renderFleet(); openArtifacts({opener:returnTarget}); toast('Artifact created',`${name} was added to ${current().title}.`);};
    },returnTarget);
  }
  function insertReference(path, returnTarget) {
    const reference=path.startsWith('.artifacts/') ? path : `.artifacts/${path}`;
    if(isPhone()){ $('#mobile-text').value=reference; updateComposerButtons($('#mobile-text'),$('#mobile-send'),$('#mobile-paste')); if(artifactPanel.classList.contains('open'))closeArtifacts(false); setMobileActive('terminal'); $('#mobile-text').focus(); }
    else { $('textarea',composer).value=reference; updateComposerButtons($('textarea',composer),$('button[type="submit"]',composer),$('#paste-only')); if(artifactPanel.classList.contains('fullscreen'))toggleArtifactFull(false); toggleCompose(true); }
    toast('Reference inserted',`${reference} is ready to send.`);
  }
  function openInsert(returnTarget) { const items=artifactItems(); if(!items.length){generic('Insert artifact reference','<p class="dim">No artifacts are available. Create an artifact first.</p>',null,returnTarget);return;} generic('Insert artifact reference',`<div class="switch-list">${items.map(item=>`<button class="switch-item" data-insert-artifact="${esc(item.path)}"><b>${esc(item.title)}</b><span>${esc(item.path)}</span></button>`).join('')}</div>`,body=>$$('[data-insert-artifact]',body).forEach(button=>button.onclick=()=>{$('#generic-dialog').close();insertReference(button.dataset.insertArtifact,returnTarget);}),returnTarget); }
  function action(name,returnTarget=document.activeElement) {
    const owner=visibleOwnerTarget(returnTarget); hideMenus();
    if(name==='view')openOutput(owner);
    if(name==='new-artifact')openNewArtifact(owner);
    if(name==='insert')openInsert(owner);
    if(name==='artifacts')artifactPanel.classList.contains('open')?closeArtifacts(true):openArtifacts({opener:owner});
    if(name==='split')cycleSplit();
    if(name==='compose')toggleCompose();
    if(name==='image')generic('Attach image','<p>Selecting files is represented by the artifact Upload control. Terminal image upload remains a production API boundary.</p><button class="primary" disabled>Choose image — placement study only</button>',null,owner);
  }

  function filterSessions() {
    const query=search.value.trim().toLowerCase(); let total=0;
    $$('.project').forEach(project=>{let shown=0;$$('.session',project).forEach(row=>{const matches=!query||`${row.dataset.title} ${row.dataset.project} ${row.dataset.branch}`.toLowerCase().includes(query);row.hidden=!matches;if(matches)shown++;});project.hidden=shown===0;total+=shown;}); $('#no-results').hidden=total!==0;
  }

  document.addEventListener('click',(event)=>{
    const row=event.target.closest('.session'); if(row)selectSession(row.dataset.branch);
    const actionButton=event.target.closest('[data-action]'); if(actionButton)action(actionButton.dataset.action,actionButton);
    const artifactButton=event.target.closest('[data-artifact-command]'); if(artifactButton)artifactCommand(artifactButton.dataset.artifactCommand,artifactButton);
    const artifactItem=event.target.closest('[data-artifact-id]'); if(artifactItem){selectedArtifactID=artifactItem.dataset.artifactId;renderArtifacts();}
    if(event.target.closest('[data-open-status]')){const owner=visibleOwnerTarget(event.target.closest('button,a')||document.activeElement);hideMenus();openStatus({opener:owner});}
    if(event.target.closest('[data-open-artifacts]'))openArtifacts({opener:event.target.closest('button,a')||document.activeElement});
    if(event.target.closest('[data-status-legend]'))generic('Status legend','<p><span class="green">● active</span> — tmux/editor/recent activity</p><p><span class="amber">● attention</span> — explicit operator flag</p><p><span class="amber">● dirty</span> — uncommitted, untracked, or unpushed</p><p style="color:var(--red)">● repair — missing/inaccessible worktree</p><p class="dim">● scan — old/stopped fast-list state</p>',null,event.target.closest('button'));
    if(!event.target.closest('.popover,#session-options,#mobile-actions,#more-actions,#artifact-menu-trigger'))hideMenus();
  });

  $('#open-nav').onclick=()=>setNav(true); $('#mobile-sessions').onclick=()=>setNav(true); scrim.onclick=()=>setNav(false);
  $('#mobile-terminal').onclick=()=>closePanelsToTerminal();
  $('#mobile-status').onclick=()=>{setNav(false,{restore:false,destination:'status'});closeArtifacts(false);openStatus({focus:true,opener:$('#mobile-status')});};
  $('#mobile-artifacts').onclick=()=>{setNav(false,{restore:false,destination:'artifacts'});closeStatus(false);openArtifacts({focus:true,opener:$('#mobile-artifacts')});};
  $('#status-toggle').onclick=()=>statusPanel.classList.contains('open')?closeStatus(true):openStatus({focus:false,opener:$('#status-toggle')});
  $('#status-close').onclick=()=>{closeStatus(true);if(isPhone())setMobileActive('terminal');};
  $('#session-options').onclick=(event)=>{event.stopPropagation();toggleMenu(sessionMenu,$('#session-options'));};
  $('#mobile-actions').onclick=(event)=>{event.stopPropagation();toggleMenu(actionsMenu,$('#mobile-actions'));};
  $('#more-actions').onclick=(event)=>{event.stopPropagation();toggleMenu(actionsMenu,$('#more-actions'));};
  $('#artifact-menu-trigger').onclick=(event)=>{event.stopPropagation();toggleMenu(artifactMenu,$('#artifact-menu-trigger'));};
  $('#output-close').onclick=closeOutput;
  $('#artifact-list-toggle').onclick=()=>artifactCommand('list',$('#artifact-list-toggle'));
  $('#artifact-fullscreen').onclick=()=>artifactCommand('fullscreen',$('#artifact-fullscreen'));
  $('#artifact-sort').onchange=(event)=>{artifactSort=event.target.value;renderArtifacts();};
  async function intakeArtifactFiles(files, source='Upload') {
    const list=[...files];
    for (const [index,file] of list.entries()) {
      const image=/\.(png|jpe?g|gif|webp)$/i.test(file.name);
      const type=image?'image':/\.(mp4|webm|mov)$/i.test(file.name)?'video':/\.html?$/i.test(file.name)?'html':/\.pdf$/i.test(file.name)?'pdf':/\.jsx?$/i.test(file.name)?'jsx':/\.md$/i.test(file.name)?'markdown':/\.(txt|log|json|csv)$/i.test(file.name)?'text':'binary';
      const content = await new Promise((resolve) => { const reader=new FileReader(); reader.onload=()=>resolve(reader.result||''); reader.onerror=()=>resolve(''); if(image)reader.readAsDataURL(file); else if(type==='text'||type==='markdown'||type==='jsx'||type==='html')reader.readAsText(file); else resolve(''); });
      current().artifacts.push(artifact(`${current().branch}-upload-${Date.now()}-${index}`,file.name,type,0,content,{summary:`${source} intake`,tags:['intake']}));
    }
    renderSession(); renderFleet(); toast(`${source} added`,`${list.length} file${list.length===1?'':'s'} added to ${current().title}.`);
  }
  $('#artifact-upload').onchange=async(event)=>{const files=[...event.target.files];event.target.value='';await intakeArtifactFiles(files);};

  let resizing=false,startY=0,startHeight=0;
  $('#artifact-resizer').addEventListener('pointerdown',(event)=>{resizing=true;startY=event.clientY;startHeight=$('#artifact-list').getBoundingClientRect().height;event.currentTarget.setPointerCapture(event.pointerId);});
  $('#artifact-resizer').addEventListener('pointermove',(event)=>{if(!resizing)return;$('#artifact-list').style.height=`${Math.max(100,Math.min(innerHeight*.55,startHeight+event.clientY-startY))}px`;});
  $('#artifact-resizer').addEventListener('pointerup',()=>{resizing=false;});

  search.oninput=filterSessions; $('#clear-filter').onclick=()=>{search.value='';filterSessions();search.focus();};
  function navigateFleet(event) {
    if (!['ArrowDown','ArrowUp','Enter'].includes(event.key)) return;
    const rows = $$('.session').filter((row) => !row.hidden && row.offsetParent !== null);
    if (!rows.length) return;
    let index = rows.indexOf(document.activeElement);
    if (event.key === 'Enter') {
      const row = index >= 0 ? rows[index] : rows[0];
      event.preventDefault();
      selectSession(row.dataset.branch, {closeNavigator:false});
      row.focus();
      return;
    }
    event.preventDefault();
    index = event.key === 'ArrowDown' ? (index < 0 ? 0 : (index + 1) % rows.length) : (index < 0 ? rows.length - 1 : (index - 1 + rows.length) % rows.length);
    rows[index].focus();
  }
  search.onkeydown=navigateFleet;

  $$('.tab').forEach((tab,index)=>{tab.tabIndex=index===0?0:-1;tab.onclick=()=>selectTab(tab);tab.onkeydown=event=>{if(!['ArrowRight','ArrowLeft'].includes(event.key))return;event.preventDefault();const tabs=$$('.tab');const next=(index+(event.key==='ArrowRight'?1:-1)+tabs.length)%tabs.length;selectTab(tabs[next]);tabs[next].focus();};});
  function selectTab(tab){$$('.tab').forEach(item=>{item.classList.remove('active');item.setAttribute('aria-selected','false');item.tabIndex=-1;});tab.classList.add('active');tab.setAttribute('aria-selected','true');tab.tabIndex=0;}

  const desktopText=$('textarea',composer); desktopText.oninput=()=>updateComposerButtons(desktopText,$('button[type="submit"]',composer),$('#paste-only'));
  composer.onsubmit=event=>{event.preventDefault();const value=desktopText.value.trim();if(!value)return;$('#terminal-content').insertAdjacentHTML('beforeend',`<div class="term-row command">❯ ${esc(value)}</div><div class="term-row dim">Input accepted by prototype fixture.</div>`);desktopText.value='';updateComposerButtons(desktopText,$('button[type="submit"]',composer),$('#paste-only'));toggleCompose(false);toast('Input sent','Composer text was appended to the terminal fixture.');};
  $('#paste-only').onclick=()=>{const value=desktopText.value.trim();if(!value)return;$('#terminal-content').insertAdjacentHTML('beforeend',`<div class="term-row command">${esc(value)}</div>`);desktopText.value='';updateComposerButtons(desktopText,$('button[type="submit"]',composer),$('#paste-only'));toggleCompose(false);};
  $('#mobile-text').oninput=()=>updateComposerButtons($('#mobile-text'),$('#mobile-send'),$('#mobile-paste'));
  $('#mobile-send').onclick=()=>{const value=$('#mobile-text').value.trim();if(!value)return;$('#terminal-content').insertAdjacentHTML('beforeend',`<div class="term-row command">❯ ${esc(value)}</div>`);$('#mobile-text').value='';updateComposerButtons($('#mobile-text'),$('#mobile-send'),$('#mobile-paste'));toast('Input sent','Mobile composer text was appended.');};
  $('#mobile-paste').onclick=()=>{const value=$('#mobile-text').value.trim();if(!value)return;$('#terminal-content').insertAdjacentHTML('beforeend',`<div class="term-row command">${esc(value)}</div>`);$('#mobile-text').value='';updateComposerButtons($('#mobile-text'),$('#mobile-send'),$('#mobile-paste'));};
  $('#keys-toggle').onclick=()=>{const open=$('#softkeys').classList.toggle('open');$('#keys-toggle').setAttribute('aria-expanded',String(open));};
  $$('#softkeys button').forEach(button=>button.onclick=()=>toast('Soft key sent',`${button.textContent} was sent to the terminal fixture.`));

  $('#rename-action').onclick=()=>{const returnTarget=$('#session-options');hideMenus();generic('Rename session',`<label class="field">Display name<input id="rename-input" value="${esc(current().title)}"></label><button class="primary" id="save-name">Save name</button>`,body=>{const input=$('#rename-input',body),save=$('#save-name',body);const validate=()=>{save.disabled=!input.value.trim();};input.oninput=validate;validate();save.onclick=()=>{current().title=input.value.trim();$('#generic-dialog').close();renderFleet();renderSession();toast('Session renamed',current().title);};},returnTarget);};
  $('#color-action').onclick=()=>{const returnTarget=$('#session-options');hideMenus();generic('Session color',`<p>Session color is a personal identifier, not status.</p><div id="color-choices" style="display:flex;gap:12px">${[['#3b82f6','Blue'],['#a855f7','Purple'],['#f97316','Orange'],['#ec4899','Pink']].map(([value,label])=>`<button class="touch" data-color="${value}" style="background:${value}" aria-label="${label}" aria-pressed="${String(current().color===value)}"></button>`).join('')}</div>`,body=>$$('[data-color]',body).forEach(button=>button.onclick=()=>{$$('[data-color]',body).forEach(choice=>choice.setAttribute('aria-pressed','false'));button.setAttribute('aria-pressed','true');current().color=button.dataset.color;renderFleet();toast('Session color updated',`${button.getAttribute('aria-label')} identifies ${current().title}.`);}),returnTarget);};
  $('#share-action').onclick=()=>{const returnTarget=$('#session-options');hideMenus();generic('Share target',`<p>Validate an existing fixture target. Token execution remains production-only.</p><label class="field">Target session<input id="share-target" autocomplete="off"></label><button class="primary" id="share-continue" disabled>Validate target</button><p id="share-result" class="dim" aria-live="polite"></p>`,body=>{const input=$('#share-target',body),button=$('#share-continue',body),result=$('#share-result',body);input.oninput=()=>{button.disabled=!input.value.trim();input.removeAttribute('aria-invalid');result.textContent='';};button.onclick=()=>{const query=input.value.trim().toLowerCase();const target=fleet.find(session=>session.branch.toLowerCase()===query||session.title.toLowerCase()===query);if(!target){input.setAttribute('aria-invalid','true');result.textContent=`No session fixture matches “${input.value.trim()}”. Choose an available session name or branch.`;input.focus();return;}result.textContent=`Validated target: ${target.title} (${target.branch})`;button.disabled=true;};},returnTarget);};
  let deleteArmed=false; $('#delete-action').onclick=()=>{if(!deleteArmed){deleteArmed=true;$('#delete-action').textContent=`Confirm delete ${current().title}`;toast('Delete armed','Select Confirm within 3 seconds.');setTimeout(()=>{deleteArmed=false;$('#delete-action').textContent='Delete session…';},3000);}else{deleteArmed=false;hideMenus();toast('Removal preview started','Static fixture retained.');}};

  $$('[data-close-dialog]').forEach(button=>button.onclick=()=>button.closest('dialog').close());
  $$('dialog').forEach(dialog=>dialog.addEventListener('close',()=>setTimeout(()=>{const target=dialogReturns.get(dialog);if(target?.isConnected&&!target.closest('[inert]')&&target.offsetParent!==null)target.focus();},0)));
  $('#new-session').onclick=()=>{$('#new-name').value='';$('#create-session').disabled=true;openDialog($('#new-dialog'),$('#new-session'));};
  const validateNew=()=>{$('#create-session').disabled=!($('#new-project').value.trim()&&$('#new-name').value.trim());}; $('#new-project').oninput=validateNew;$('#new-name').oninput=validateNew;
  $('#new-form').onsubmit=event=>{event.preventDefault();validateNew();if($('#create-session').disabled)return;const name=$('#new-name').value.trim(),project=$('#new-project').value.trim(),target=$('#new-target').value;let branch=name.toLowerCase().replace(/[^a-z0-9]+/g,'-').replace(/^-|-$/g,'')||`session-${nextCreated}`;while(fixtures[branch])branch=`${branch}-${++nextCreated}`;const item=fixture(name,project,branch,'idle',target,fleet.length,{routes:[],artifacts:[],reason:'new fixture created; no running tmux session yet'});fleet.push(item);fixtures[branch]=item;$('#new-dialog').close();activeBranch=branch;renderFleet();renderSession();toast('Session fixture created',`${name} was added and selected.`);};

  function showRemoteImageToast(){const item=toast('Remote image received','operator-console-preview.png from the CLI fixture',{duration:0});item.innerHTML=`<div class="toast-media"><img alt="Remote CLI image preview" src="${svgPreview('Remote image')}"><div><b>Remote image received</b><span>operator-console-preview.png</span></div></div><div class="toast-actions"><button data-open-image>Open preview</button><button data-dismiss-toast>Dismiss</button></div>`;$('[data-dismiss-toast]',item).onclick=()=>item.remove();$('[data-open-image]',item).onclick=()=>generic('Remote image preview',`<img src="${svgPreview('Remote image')}" alt="Expanded remote CLI image preview" style="max-width:100%">`,null,$('[data-open-image]',item));return item;}
  function showFlagToast(){const item=toast('Session flagged','Gatepost proxy consolidation — proxy route needs operator review.',{flag:true,duration:0});item.innerHTML='<b>Session flagged</b><span>Gatepost proxy consolidation — proxy route needs operator review.</span><div class="toast-actions"><button data-view-flag>View session</button><button data-dismiss-toast>Dismiss</button></div>';$('[data-dismiss-toast]',item).onclick=()=>item.remove();$('[data-view-flag]',item).onclick=()=>{selectSession('jf-gatepost-proxy',{feedback:true});item.remove();};return item;}
  function presentGalleryToast(showToast){if(isCompact()&&nav.classList.contains('open'))setNav(false,{restore:false});const item=showToast();setTimeout(()=>$('.toast-actions button',item)?.focus(),0);}
  $('#stale-launch').onclick=()=>openDialog($('#stale-dialog'),$('#stale-launch')); $('#state-gallery').onclick=()=>openDialog($('#states-dialog'),$('#state-gallery'));
  $('#show-image-toast').onclick=()=>{$('#states-dialog').close();presentGalleryToast(showRemoteImageToast);}; $('#show-flag-toast').onclick=()=>{$('#states-dialog').close();presentGalleryToast(showFlagToast);};
  $('#prune-action').onclick=event=>{const confirming=event.currentTarget.textContent.startsWith('Confirm');event.currentTarget.textContent=confirming?'Pruning preview complete ✓':'Confirm prune 1 clean';if(confirming)event.currentTarget.disabled=true;};
  $('#repair-action').onclick=()=>{$('#stale-dialog').close();selectSession('jf-mcp-audit',{feedback:true});}; $('#reviewed-action').onclick=event=>{event.currentTarget.textContent='Reviewed ✓';event.currentTarget.disabled=true;};
  $('#quick-input').oninput=event=>{const items=$$('#quick-dialog .switch-item');items.forEach(item=>{item.hidden=!item.textContent.toLowerCase().includes(event.target.value.toLowerCase());item.classList.remove('selected');});items.find((item)=>!item.hidden)?.classList.add('selected');};
  $('#quick-dialog').onkeydown=event=>{if(!['ArrowDown','ArrowUp','Enter'].includes(event.key))return;const items=$$('#quick-dialog .switch-item').filter((item)=>!item.hidden);if(!items.length)return;let index=items.findIndex((item)=>item.classList.contains('selected'));if(event.key==='Enter'){event.preventDefault();const item=items[Math.max(0,index)];$('#quick-dialog').close();selectSession(item.dataset.branch,{feedback:true});return;}event.preventDefault();items.forEach((item)=>item.classList.remove('selected'));index=event.key==='ArrowDown'?(index+1)%items.length:(index<=0?items.length-1:index-1);items[index].classList.add('selected');items[index].scrollIntoView({block:'nearest'});};
  function openQuick(){renderQuick();$('#quick-input').value='';openDialog($('#quick-dialog'),document.activeElement);setTimeout(()=>$('#quick-input').focus(),0);}
  $('#quick-dialog').addEventListener('click',event=>{const item=event.target.closest('.switch-item');if(item){$('#quick-dialog').close();selectSession(item.dataset.branch,{feedback:true});}});

  artifactPanel.addEventListener('paste',event=>{
    if(!artifactPanel.classList.contains('open'))return;
    const files=[...event.clipboardData.files];
    if(files.length){event.preventDefault();intakeArtifactFiles(files,'Paste');return;}
    const text=event.clipboardData.getData('text/plain').trim();
    if(text&&!isEditable(event.target)){event.preventDefault();const name=`pasted-note-${artifactItems().length+1}.md`;const item=artifact(`${current().branch}-paste-${Date.now()}`,name,'markdown',0,text,{summary:'Pasted into ArtifactPane',tags:['paste']});current().artifacts.push(item);selectedArtifactID=item.id;renderSession();renderFleet();toast('Paste added',`${name} was added to ${current().title}.`);}
  });
  artifactPanel.addEventListener('dragover',event=>{event.preventDefault();event.stopPropagation();$('#artifact-preview').classList.add('dragging');});
  artifactPanel.addEventListener('dragleave',event=>{if(!artifactPanel.contains(event.relatedTarget))$('#artifact-preview').classList.remove('dragging');});
  artifactPanel.addEventListener('drop',event=>{event.preventDefault();event.stopPropagation();$('#artifact-preview').classList.remove('dragging');intakeArtifactFiles(event.dataTransfer.files,'Drop');});
  $('#stage').addEventListener('dragenter',event=>{event.preventDefault();$('#stage').classList.add('dragging');}); $('#stage').addEventListener('dragover',event=>event.preventDefault()); $('#stage').addEventListener('dragleave',event=>{if(!$('#stage').contains(event.relatedTarget))$('#stage').classList.remove('dragging');}); $('#stage').addEventListener('drop',event=>{event.preventDefault();$('#stage').classList.remove('dragging');const file=[...event.dataTransfer.files].find(item=>item.type.startsWith('image/'));toast(file?'Image accepted':'Image not accepted',file?`${file.name} reached static confirmation.`:'Drop a PNG, JPG, GIF, or WebP image.');});

  document.addEventListener('keydown',event=>{
    if(!outputReader.hidden){
      if(event.key==='Escape'){event.preventDefault();closeOutput();return;}
      if(event.key==='Tab'){
        const items=[$('#output-reader-heading'),...focusables(outputReader)],first=items[0],last=items[items.length-1];
        if(event.shiftKey&&document.activeElement===first){event.preventDefault();last.focus();}
        else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first.focus();}
      }
      return;
    }
    if(artifactPanel.classList.contains('fullscreen')&&!$$('dialog[open]').length){
      if(event.key==='Escape'){
        event.preventDefault();
        if(!artifactMenu.hidden){hideMenus({restore:true});return;}
        toggleArtifactFull(false);return;
      }
      if(event.key==='Tab'){
        const heading=$('#artifact-heading'),tabItems=focusables(artifactPanel),first=tabItems[0],last=tabItems[tabItems.length-1];
        if(event.shiftKey&&(document.activeElement===heading||document.activeElement===first)){event.preventDefault();last.focus();}
        else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();heading.focus();}
      }
      return;
    }
    if((event.metaKey||event.ctrlKey)&&!event.shiftKey&&event.key.toLowerCase()==='p'){event.preventDefault();$('#quick-dialog').open?$('#quick-dialog').close():openQuick();return;}
    if((event.metaKey||event.ctrlKey)&&event.key.toLowerCase()==='k'){event.preventDefault();toggleCompose();return;}
    if(event.key==='/'&&!isEditable(event.target)&&!$$('dialog[open]').length){event.preventDefault();innerWidth<1024?setNav(true):search.focus();return;}
    if(event.key==='Escape'){
      if($$('dialog[open]').length)return;
      if(!sessionMenu.hidden||!actionsMenu.hidden||!artifactMenu.hidden){event.preventDefault();hideMenus({restore:true});return;}
      if(artifactPanel.classList.contains('fullscreen')){event.preventDefault();toggleArtifactFull(false);return;}
      if(nav.classList.contains('open')){event.preventDefault();setNav(false);return;}
      if(composer.classList.contains('open')){event.preventDefault();toggleCompose(false);return;}
      if(statusPanel.classList.contains('open')){event.preventDefault();closeStatus(true);if(isPhone())setMobileActive('terminal');return;}
      if(artifactPanel.classList.contains('open')){event.preventDefault();closeArtifacts(true);if(isPhone())setMobileActive('terminal');return;}
    }
    if(nav.classList.contains('open')&&event.key==='Tab'){const items=focusables(nav);if(!items.length)return;const first=items[0],last=items[items.length-1];if(event.shiftKey&&document.activeElement===first){event.preventDefault();last.focus();}else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first.focus();}}
  });

  addEventListener('resize',()=>{if(innerWidth>=1024&&nav.classList.contains('open'))setNav(false,{restore:false});if(isCompact()&&statusPanel.classList.contains('open')&&artifactPanel.classList.contains('open'))closeArtifacts(false);syncNavigatorMode();syncPanelIsolation();});
  renderFleet(); renderSession(); syncNavigatorMode(); syncPanelIsolation();
})();

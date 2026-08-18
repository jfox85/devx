(() => {
  const $ = (selector, root = document) => root.querySelector(selector);
  const $$ = (selector, root = document) => [...root.querySelectorAll(selector)];
  const esc = (value) => String(value).replace(/[&<>"']/g, (char) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char]));
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
  const dialogReturns = new WeakMap();
  let navReturn = null;
  let navPreviousDestination = 'terminal';
  let menuReturn = null;
  let activeBranch = 'jf-ui-refresh';
  let split = 'terminal';
  let composerReturn = null;
  let panelReturn = null;
  let nextCreated = 1;

  const fixtures = {
    'jf-ui-refresh': {title:'Operator console refresh', project:'devx', branch:'jf-ui-refresh', status:'active', statusTone:'green', tmux:'active', target:'host', dirty:true, reason:'tmux is running; worktree has uncommitted changes', routes:[['ui','operator-console-ui.localhost'],['api','operator-console-api.localhost'],['docs','operator-console-docs.localhost']], artifacts:[['operator-console.png','image'],['ui-review.md','markdown'],['verification.txt','text']], gatepost:null, color:'#a855f7'},
    'jf-gatepost-proxy': {title:'Gatepost proxy consolidation', project:'devx', branch:'jf-gatepost-proxy', status:'attention', statusTone:'amber', tmux:'stopped', target:'gatepost', dirty:false, reason:'flagged: proxy route needs operator review', routes:[['api','gatepost-proxy-api.localhost'],['logs','gatepost-proxy-logs.localhost']], artifacts:[['proxy-review.md','markdown']], gatepost:'https://gatepost-proxy-logs.localhost'},
    'jf-artifact-search': {title:'Artifact search indexing', project:'devx', branch:'jf-artifact-search', status:'dirty', statusTone:'amber', tmux:'active', target:'host', dirty:true, reason:'worktree has uncommitted, untracked, or unpushed work', routes:[['ui','artifact-search-ui.localhost']], artifacts:[['index-plan.md','markdown'],['sample-index.json','json'],['search-results.txt','text'],['artifact-grid.png','image'],['verification.md','markdown']], gatepost:null},
    'jf-reconnect': {title:'Terminal reconnect handling', project:'devx', branch:'jf-reconnect', status:'idle', statusTone:'muted', tmux:'stopped', target:'docker', dirty:false, reason:'no running tmux session or recent activity', routes:[], artifacts:[], gatepost:null},
    'jf-unread-audit': {title:'Unread semantics audit', project:'media-memory', branch:'jf-unread-audit', status:'active', statusTone:'green', tmux:'active', target:'host', dirty:false, reason:'tmux is running or recent activity was observed', routes:[['ui','unread-audit-ui.localhost']], artifacts:[['audit-notes.md','markdown'],['states.png','image']], gatepost:null},
    'jf-mcp-audit': {title:'Privacy-safe MCP logging', project:'media-memory', branch:'jf-mcp-audit', status:'repair', statusTone:'red', tmux:'unknown', target:'host', dirty:null, reason:'worktree is missing or inaccessible; git state is unknown', routes:[], artifacts:[], gatepost:null},
    'jf-dashboard-states': {title:'Dashboard response states', project:'nibit', branch:'jf-dashboard-states', status:'active', statusTone:'green', tmux:'active', target:'docker', dirty:false, reason:'tmux is running or recent activity was observed', routes:[['ui','dashboard-states-ui.localhost'],['api','dashboard-states-api.localhost']], artifacts:[['response-grid.png','image'],['edge-cases.md','markdown']], gatepost:null}
  };

  const current = () => fixtures[activeBranch];
  const isEditable = (element) => Boolean(element?.closest('input,textarea,select,[contenteditable="true"]'));
  const focusables = (root) => $$('button:not([disabled]),a[href],input:not([disabled]),textarea:not([disabled]),select:not([disabled]),[tabindex]:not([tabindex="-1"])', root).filter((element) => !element.hidden && element.offsetParent !== null);
  const isPhone = () => innerWidth <= 600;
  const isCompact = () => innerWidth <= 1180;

  function toneColor(tone) {
    return tone === 'green' ? 'var(--green)' : tone === 'amber' ? 'var(--amber)' : tone === 'red' ? 'var(--red)' : 'var(--muted)';
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
    $('#session-facts').innerHTML = `<div class="fact"><span class="${session.tmux === 'active' ? 'good' : ''}">●</span> <b>tmux ${esc(session.tmux)}</b></div><div class="fact"><b>${esc(session.target)}</b> target</div><div class="fact">${dirty}</div><div class="fact"><button class="fact-button" data-open-status>${session.routes.length} ${session.routes.length === 1 ? 'route' : 'routes'}</button></div><div class="fact"><button class="fact-button" data-open-artifacts>${session.artifacts.length} ${session.artifacts.length === 1 ? 'artifact' : 'artifacts'}</button></div>`;

    $('#artifact-list').innerHTML = session.artifacts.length ? session.artifacts.map(([name,type]) => `<div class="artifact-card"><b>${esc(name)}</b><span>${esc(type)} artifact</span></div>`).join('') : '<div class="detail"><p class="dim">No artifacts in this session fixture.</p></div>';

    const statusClass = session.statusTone === 'red' ? 'style="color:var(--red)"' : `class="${session.statusTone === 'green' ? 'green' : session.statusTone === 'amber' ? 'amber' : 'dim'}"`;
    const routes = session.routes.length ? session.routes.map(([name,address]) => `<a class="route-link" href="https://${esc(address)}" target="_blank" rel="noreferrer">↗ ${esc(name)} · ${esc(address)}</a>`).join('') : '<p class="dim">No route addresses in this session fixture.</p>';
    const gatepost = session.gatepost ? `<p>Enabled for this session.</p><a class="route-link" href="${esc(session.gatepost)}" target="_blank" rel="noreferrer">↗ Open external Gatepost logs</a>` : '<p>Not enabled for this session.</p>';
    $('#status-content').innerHTML = `<div class="detail"><h2>Status</h2><p><span ${statusClass}>● ${esc(session.status)}</span> — ${esc(session.reason)}.</p><button class="small-btn" data-status-legend>Status legend</button></div><div class="detail"><h2>Target</h2><p><b>${esc(session.target)}</b></p><p class="dim">Fixture target for this selected session.</p></div><div class="detail"><h2>Routes · addresses only</h2>${routes}<p class="dim">No health claim is inferred from route presence.</p></div><div class="detail"><h2>Gatepost</h2>${gatepost}</div><div class="detail"><h2>Artifacts</h2><p>${session.artifacts.length} session ${session.artifacts.length === 1 ? 'artifact' : 'artifacts'}</p><button class="small-btn" data-open-artifacts>Open artifact pane</button></div>`;

    $('#terminal-content').innerHTML = `<div class="term-row dim">Session fixture loaded for ${esc(session.project)}</div><div class="term-row"><span class="cyan">${esc(session.project)}</span> <b>${esc(session.branch)}</b> <span class="cyan">git:(${esc(session.branch)})</span>${session.dirty ? ' <span class="amber">±</span>' : ''}</div><div class="term-row command">❯ devx status --session ${esc(session.branch)}</div><div class="term-row"><span class="dim">STATUS</span>  <span style="color:${toneColor(session.statusTone)}">${esc(session.status)}</span></div><div class="term-row"><span class="dim">TARGET</span>  ${esc(session.target)}</div><div class="term-row"><span class="dim">TMUX</span>    ${esc(session.tmux)}</div><div class="term-row"><span class="dim">ROUTES</span>  ${session.routes.length ? session.routes.map((route) => esc(route[0])).join(', ') : 'none'}</div><div class="term-row"> </div><div class="term-row"><span class="cyan">${esc(session.project)}</span> <b>${esc(session.branch)}</b> <span class="cyan">git:(${esc(session.branch)})</span></div><div class="term-row command">❯ <span class="cursor"></span></div>`;
  }

  function selectSession(branch, {closeNavigator = true, feedback = false} = {}) {
    if (!fixtures[branch]) return false;
    activeBranch = branch;
    $$('.session').forEach((row) => {
      const selected = row.dataset.branch === branch;
      row.classList.toggle('selected', selected);
      selected ? row.setAttribute('aria-current','true') : row.removeAttribute('aria-current');
    });
    $$('#quick-dialog .switch-item').forEach((item) => {
      const selected = item.dataset.branch === branch;
      item.classList.toggle('selected', selected);
      selected ? item.setAttribute('aria-current','true') : item.removeAttribute('aria-current');
    });
    renderSession();
    if (isPhone()) {
      closeStatus(false);
      closeArtifacts(false);
      setMobileActive('terminal');
    }
    if (closeNavigator && innerWidth < 1024 && nav.classList.contains('open')) setNav(false, {restore:false, destination:'terminal'});
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

  function activeMobileDestination() {
    return $('.mobile-tab.active')?.id.replace('mobile-','') || 'terminal';
  }

  function syncNavigatorMode() {
    if (innerWidth < 1024) {
      const open = nav.classList.contains('open');
      nav.inert = !open;
      nav.setAttribute('aria-hidden', String(!open));
      if (open) {
        nav.setAttribute('role','dialog');
        nav.setAttribute('aria-modal','true');
      } else {
        nav.removeAttribute('role');
        nav.removeAttribute('aria-modal');
      }
    } else {
      nav.classList.remove('open');
      scrim.classList.remove('open');
      nav.inert = false;
      nav.removeAttribute('aria-hidden');
      nav.removeAttribute('role');
      nav.removeAttribute('aria-modal');
      workspace.inert = false;
      $('.mobile-bottom').inert = false;
      $('#open-nav').setAttribute('aria-expanded','false');
    }
  }

  function setNav(open, {restore = true, destination} = {}) {
    if (open) {
      navPreviousDestination = activeMobileDestination();
      navReturn = document.activeElement;
      nav.classList.add('open');
      scrim.classList.add('open');
      workspace.inert = true;
      $('.mobile-bottom').inert = true;
      setMobileActive('sessions');
    } else {
      nav.classList.remove('open');
      scrim.classList.remove('open');
      workspace.inert = false;
      $('.mobile-bottom').inert = false;
      setMobileActive(destination || navPreviousDestination);
    }
    $('#open-nav').setAttribute('aria-expanded', String(open));
    syncNavigatorMode();
    if (open) setTimeout(() => search.focus(), 0);
    else if (restore) setTimeout(() => navReturn?.focus(), 0);
  }

  function syncPanelIsolation() {
    const statusOpen = statusPanel.classList.contains('open');
    const artifactOpen = artifactPanel.classList.contains('open');
    if (!isPhone()) {
      ['.topbar','.facts','.windowbar','#stage','.mobile-composer','.terminal-pane','#composer'].forEach((selector) => { const element = $(selector); if (element) element.inert = false; });
      return;
    }
    $('.topbar').inert = statusOpen || artifactOpen;
    $('.facts').inert = statusOpen || artifactOpen;
    $('.windowbar').inert = statusOpen || artifactOpen;
    $('.mobile-composer').inert = statusOpen || artifactOpen;
    $('#stage').inert = statusOpen;
    $('.terminal-pane').inert = artifactOpen;
    composer.inert = statusOpen || artifactOpen;
  }

  function openStatus({focus = isPhone(), opener = document.activeElement} = {}) {
    panelReturn = opener;
    if (isCompact()) closeArtifacts(false);
    statusPanel.classList.add('open');
    $('#status-toggle').setAttribute('aria-expanded','true');
    if (isPhone()) setMobileActive('status');
    syncPanelIsolation();
    if (focus) setTimeout(() => $('#status-heading').focus(), 0);
  }

  function closeStatus(restore = false) {
    const wasOpen = statusPanel.classList.contains('open');
    statusPanel.classList.remove('open');
    $('#status-toggle').setAttribute('aria-expanded','false');
    syncPanelIsolation();
    if (restore && wasOpen) setTimeout(() => panelReturn?.focus(), 0);
  }

  function applySplit(mode) {
    split = mode;
    pane.className = 'pane-wrap';
    artifactPanel.classList.toggle('open', mode !== 'terminal');
    if (mode === 'horizontal') pane.classList.add('horizontal');
    if (mode === 'artifacts') pane.classList.add('artifacts-only');
    $('#split-label').textContent = ` ${mode}`;
    $$('[data-action="artifacts"]').forEach((button) => button.classList.toggle('active', mode !== 'terminal'));
    syncPanelIsolation();
  }

  function openArtifacts({focus = isPhone(), opener = document.activeElement} = {}) {
    panelReturn = opener;
    if (isCompact()) closeStatus(false);
    applySplit(split === 'terminal' ? 'vertical' : split);
    if (isPhone()) setMobileActive('artifacts');
    if (focus) setTimeout(() => $('#artifact-heading').focus(), 0);
  }

  function closeArtifacts(restore = false) {
    const wasOpen = artifactPanel.classList.contains('open');
    applySplit('terminal');
    if (restore && wasOpen) setTimeout(() => panelReturn?.focus(), 0);
  }

  function cycleSplit() {
    const modes = ['terminal','vertical','horizontal','artifacts'];
    closeStatus(false);
    applySplit(modes[(modes.indexOf(split) + 1) % modes.length]);
  }

  function syncResponsiveActions() {
    const splitAction = $('#actions-menu [data-action="split"]');
    splitAction.textContent = isPhone() ? 'Show artifacts' : 'Change split mode';
  }

  function closePanelsToTerminal(restore = false) {
    closeStatus(false);
    closeArtifacts(false);
    setMobileActive('terminal');
    syncPanelIsolation();
    if (restore) setTimeout(() => $('#mobile-terminal').focus(), 0);
  }

  function updateComposerButtons(textarea, send, paste) {
    const hasText = textarea.value.trim().length > 0;
    send.disabled = !hasText;
    paste.disabled = !hasText;
  }

  function toggleCompose(force) {
    const open = force ?? !composer.classList.contains('open');
    if (open) composerReturn = document.activeElement;
    composer.classList.toggle('open', open);
    if (open) setTimeout(() => $('textarea', composer).focus(), 0);
    else if (force === false && composerReturn && document.activeElement?.closest('#composer')) setTimeout(() => composerReturn.focus(), 0);
  }

  function visibleOwnerTarget(target = document.activeElement) {
    if (target?.closest?.('#session-menu')) return $('#session-options');
    if (target?.closest?.('#actions-menu')) return $('#mobile-actions');
    return target;
  }

  function openDialog(dialog, returnTarget = document.activeElement) {
    dialogReturns.set(dialog, visibleOwnerTarget(returnTarget));
    if (!dialog.open) dialog.showModal();
  }

  function generic(title, html, setup, returnTarget = document.activeElement) {
    $('#generic-title').textContent = title;
    $('#generic-body').innerHTML = html;
    openDialog($('#generic-dialog'), returnTarget);
    setup?.($('#generic-body'));
  }

  function toast(title, text, {flag = false, duration = 5000} = {}) {
    const item = document.createElement('div');
    item.className = `toast${flag ? ' flag' : ''}`;
    item.setAttribute('role','status');
    item.innerHTML = `<b>${esc(title)}</b><span>${esc(text)}</span>`;
    $('#toasts').append(item);
    if (duration) setTimeout(() => item.remove(), duration);
    return item;
  }

  function showRemoteImageToast() {
    const item = document.createElement('div');
    item.className = 'toast';
    item.setAttribute('role','status');
    const preview = 'data:image/svg+xml,' + encodeURIComponent('<svg xmlns="http://www.w3.org/2000/svg" width="160" height="120"><rect width="160" height="120" fill="#082f49"/><rect x="22" y="20" width="116" height="80" rx="8" fill="#0ea5e9"/><circle cx="55" cy="48" r="11" fill="#fbbf24"/><path d="M32 88l30-28 22 18 18-14 28 24" fill="none" stroke="#eef5ff" stroke-width="6"/></svg>');
    item.innerHTML = `<div class="toast-media"><img alt="Remote CLI image preview" src="${preview}"><div><b>Remote image received</b><span>operator-console-preview.png from the CLI fixture</span></div></div><div class="toast-actions"><button data-open-image>Open preview</button><button data-dismiss-toast>Dismiss</button></div>`;
    $('#toasts').append(item);
    $('[data-dismiss-toast]', item).onclick = () => item.remove();
    $('[data-open-image]', item).onclick = () => generic('Remote image preview', `<img src="${preview}" alt="Expanded remote CLI image preview" style="display:block;max-width:100%;margin:auto;border-radius:8px">`, null, $('[data-open-image]', item));
    return item;
  }

  function showFlagToast() {
    const item = document.createElement('div');
    item.className = 'toast flag';
    item.setAttribute('role','status');
    item.innerHTML = '<b>Session flagged</b><span>Gatepost proxy consolidation — proxy route needs operator review.</span><div class="toast-actions"><button data-view-flag>View session</button><button data-dismiss-toast>Dismiss</button></div>';
    $('#toasts').append(item);
    $('[data-dismiss-toast]', item).onclick = () => item.remove();
    $('[data-view-flag]', item).onclick = () => { selectSession('jf-gatepost-proxy', {feedback:true}); item.remove(); };
    return item;
  }

  function presentGalleryToast(showToast) {
    if (isCompact() && nav.classList.contains('open')) setNav(false, {restore:false});
    const item = showToast();
    setTimeout(() => $('.toast-actions button', item)?.focus(), 0);
  }

  function hideMenus({restore = false} = {}) {
    const hadOpen = !sessionMenu.hidden || !actionsMenu.hidden;
    sessionMenu.hidden = true;
    actionsMenu.hidden = true;
    $('#session-options').setAttribute('aria-expanded','false');
    $('#mobile-actions').setAttribute('aria-expanded','false');
    if (restore && hadOpen) setTimeout(() => menuReturn?.focus(), 0);
  }

  function toggleMenu(menu, button) {
    const opening = menu.hidden;
    hideMenus();
    if (!opening) return;
    menuReturn = button;
    menu.hidden = false;
    button.setAttribute('aria-expanded','true');
    setTimeout(() => $('button:not([disabled])', menu)?.focus(), 0);
  }

  function openNewArtifact(returnTarget) {
    generic('New artifact', '<label class="field">Artifact text<textarea id="artifact-text" rows="6" placeholder="Paste notes, logs, or a handoff…"></textarea></label><button class="primary" id="create-artifact" disabled>Create artifact</button>', (body) => {
      const input = $('#artifact-text', body);
      const create = $('#create-artifact', body);
      input.oninput = () => { create.disabled = !input.value.trim(); };
      create.onclick = () => {
        const session = current();
        const name = `note-${session.artifacts.length + 1}.txt`;
        session.artifacts.push([name,'text']);
        renderSession();
        $('#generic-dialog').close();
        toast('Artifact created', `${name} was added to ${session.title}.`);
      };
    }, returnTarget);
  }

  function openInsert(returnTarget) {
    const artifacts = current().artifacts;
    if (!artifacts.length) {
      generic('Insert artifact reference','<p class="dim">No artifacts are available for this session. Create an artifact first.</p><button class="primary" disabled>Insert unavailable</button>', null, returnTarget);
      return;
    }
    generic('Insert artifact reference', `<div class="switch-list">${artifacts.map(([name]) => `<button class="switch-item" data-insert-artifact="${esc(name)}"><b>${esc(name)}</b><span>Insert path into terminal composer</span></button>`).join('')}</div>`, (body) => {
      $$('[data-insert-artifact]', body).forEach((button) => button.onclick = () => {
        const reference = `[artifact:${button.dataset.insertArtifact}]`;
        if (isPhone()) {
          $('#mobile-text').value = reference;
          updateComposerButtons($('#mobile-text'), $('#mobile-send'), $('#mobile-paste'));
          closePanelsToTerminal();
          $('#generic-dialog').close();
          $('#mobile-text').focus();
        } else {
          $('textarea', composer).value = reference;
          updateComposerButtons($('textarea', composer), $('button[type="submit"]', composer), $('#paste-only'));
          $('#generic-dialog').close();
          toggleCompose(true);
        }
        toast('Reference inserted', `${button.dataset.insertArtifact} is ready to send.`);
      });
    }, returnTarget);
  }

  function action(name, returnTarget = document.activeElement) {
    const owner = visibleOwnerTarget(returnTarget);
    hideMenus();
    if (name === 'view') generic('Terminal output view', `<p>Copy-friendly output for ${esc(current().title)}.</p><pre class="mono dim" style="white-space:pre-wrap">${esc($('#terminal-content').innerText)}</pre>`, null, owner);
    if (name === 'new-artifact') openNewArtifact(owner);
    if (name === 'insert') openInsert(owner);
    if (name === 'artifacts') artifactPanel.classList.contains('open') ? closeArtifacts(true) : openArtifacts({opener:owner});
    if (name === 'split') {
      if (isPhone()) openArtifacts({opener:owner});
      else cycleSplit();
    }
    if (name === 'compose') toggleCompose();
    if (name === 'image') generic('Attach image', '<p>This static prototype validates placement only. File selection and upload require the production API.</p><button class="primary" disabled>Choose image — placement study only</button>', null, owner);
  }

  function addCreatedSession(project, name, target) {
    const branchBase = name.toLowerCase().trim().replace(/[^a-z0-9]+/g,'-').replace(/^-|-$/g,'') || `session-${nextCreated}`;
    let branch = branchBase;
    while (fixtures[branch]) branch = `${branchBase}-${++nextCreated}`;
    fixtures[branch] = {title:name.trim(), project:project.trim(), branch, status:'idle', statusTone:'muted', tmux:'stopped', target, dirty:false, reason:'new fixture created; no running tmux session yet', routes:[], artifacts:[], gatepost:null};
    const targetProject = $$('.project').find((section) => $('.project-toggle', section)?.textContent.includes(project.trim())) || $('.project');
    const sessions = $('.sessions', targetProject);
    const row = document.createElement('button');
    row.className = 'session';
    row.dataset.title = name.trim(); row.dataset.project = project.trim(); row.dataset.branch = branch; row.dataset.status = 'idle';
    row.innerHTML = `<span class="status-dot"></span><span class="session-main"><span class="session-name">${esc(name.trim())}</span><span class="branch">${esc(branch)}</span><span class="meta"><span class="badge">idle</span><span class="badge">${esc(target)}</span></span></span>`;
    sessions.append(row);
    const count = $('.count', targetProject);
    count.textContent = String(Number(count.textContent) + 1);
    $('.summary span:last-child b').textContent = String($$('.session').length);
    selectSession(branch, {closeNavigator:false});
  }

  document.addEventListener('click', (event) => {
    const sessionRow = event.target.closest('.session');
    if (sessionRow) selectSession(sessionRow.dataset.branch);
    const actionButton = event.target.closest('[data-action]');
    if (actionButton) action(actionButton.dataset.action, actionButton);
    if (event.target.closest('[data-open-status]')) {
      const owner = visibleOwnerTarget(event.target.closest('button,a') || document.activeElement);
      hideMenus();
      openStatus({opener:owner});
    }
    if (event.target.closest('[data-open-artifacts]')) openArtifacts({opener:event.target.closest('button,a') || document.activeElement});
    if (event.target.closest('[data-status-legend]')) generic('Status legend','<p><span class="green">● active</span> — tmux/editor/recent activity</p><p><span class="amber">● flagged</span> — explicit attention reason</p><p><span class="amber">● dirty</span> — uncommitted, untracked, or unpushed</p><p style="color:var(--red)">● repair — missing/inaccessible worktree or unknown git state</p><p class="dim">● cleanup — full scan says safe to prune</p>', null, event.target.closest('button'));
    if (!event.target.closest('.popover,#session-options,#mobile-actions')) hideMenus();
  });

  $('#open-nav').onclick = () => setNav(true);
  $('#mobile-sessions').onclick = () => setNav(true);
  scrim.onclick = () => setNav(false);
  $('#mobile-terminal').onclick = () => closePanelsToTerminal();
  $('#mobile-status').onclick = () => { setNav(false, {restore:false,destination:'status'}); closeArtifacts(false); openStatus({focus:true,opener:$('#mobile-status')}); };
  $('#mobile-artifacts').onclick = () => { setNav(false, {restore:false,destination:'artifacts'}); closeStatus(false); openArtifacts({focus:true,opener:$('#mobile-artifacts')}); };
  $('#status-toggle').onclick = () => statusPanel.classList.contains('open') ? closeStatus(true) : openStatus({focus:false,opener:$('#status-toggle')});
  $('#status-close').onclick = () => { closeStatus(true); if (isPhone()) setMobileActive('terminal'); };
  $('#session-options').onclick = (event) => { event.stopPropagation(); toggleMenu(sessionMenu, $('#session-options')); };
  $('#mobile-actions').onclick = (event) => { event.stopPropagation(); toggleMenu(actionsMenu, $('#mobile-actions')); };

  $$('.project-toggle').forEach((button) => button.onclick = () => {
    const project = button.closest('.project');
    const collapsed = project.classList.toggle('collapsed');
    button.setAttribute('aria-expanded', String(!collapsed));
  });

  function filterSessions() {
    const query = search.value.trim().toLowerCase();
    let total = 0;
    $$('.project').forEach((project) => {
      let shown = 0;
      $$('.session', project).forEach((row) => {
        const matches = !query || `${row.dataset.title} ${row.dataset.project} ${row.dataset.branch}`.toLowerCase().includes(query);
        row.hidden = !matches;
        if (matches) shown += 1;
      });
      project.hidden = shown === 0;
      total += shown;
    });
    $('#no-results').hidden = total !== 0;
  }
  search.oninput = filterSessions;
  $('#clear-filter').onclick = () => { search.value = ''; filterSessions(); search.focus(); };

  $$('.tab').forEach((tab, index) => {
    tab.onclick = () => selectTab(tab);
    tab.onkeydown = (event) => {
      if (!['ArrowRight','ArrowLeft'].includes(event.key)) return;
      event.preventDefault();
      const tabs = $$('.tab');
      const next = (index + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length;
      selectTab(tabs[next]); tabs[next].focus();
    };
  });
  function selectTab(tab) {
    $$('.tab').forEach((item) => { item.classList.remove('active'); item.setAttribute('aria-selected','false'); item.tabIndex = -1; });
    tab.classList.add('active'); tab.setAttribute('aria-selected','true'); tab.tabIndex = 0;
  }
  $$('.tab').forEach((tab, index) => tab.tabIndex = index === 0 ? 0 : -1);

  const desktopText = $('textarea', composer);
  desktopText.oninput = () => updateComposerButtons(desktopText, $('button[type="submit"]', composer), $('#paste-only'));
  composer.onsubmit = (event) => {
    event.preventDefault();
    const value = desktopText.value.trim();
    if (!value) return;
    $('#terminal-content').insertAdjacentHTML('beforeend', `<div class="term-row command">❯ ${esc(value)}</div><div class="term-row dim">Input accepted by prototype fixture.</div>`);
    desktopText.value = '';
    updateComposerButtons(desktopText, $('button[type="submit"]', composer), $('#paste-only'));
    toggleCompose(false);
    toast('Input sent', 'Composer text was appended to the active terminal fixture.');
  };
  $('#paste-only').onclick = () => {
    const value = desktopText.value.trim();
    if (!value) return;
    $('#terminal-content').insertAdjacentHTML('beforeend', `<div class="term-row command">${esc(value)}</div>`);
    desktopText.value = '';
    updateComposerButtons(desktopText, $('button[type="submit"]', composer), $('#paste-only'));
    toggleCompose(false);
    toast('Input pasted', 'Text was appended without a submit marker.');
  };

  $('#mobile-text').oninput = () => updateComposerButtons($('#mobile-text'), $('#mobile-send'), $('#mobile-paste'));
  $('#mobile-send').onclick = () => {
    const value = $('#mobile-text').value.trim();
    if (!value) return;
    $('#terminal-content').insertAdjacentHTML('beforeend', `<div class="term-row command">❯ ${esc(value)}</div><div class="term-row dim">Input accepted by prototype fixture.</div>`);
    $('#mobile-text').value = '';
    updateComposerButtons($('#mobile-text'), $('#mobile-send'), $('#mobile-paste'));
    toast('Input sent', 'Mobile composer text was appended to the terminal fixture.');
  };
  $('#mobile-paste').onclick = () => {
    const value = $('#mobile-text').value.trim();
    if (!value) return;
    $('#terminal-content').insertAdjacentHTML('beforeend', `<div class="term-row command">${esc(value)}</div>`);
    $('#mobile-text').value = '';
    updateComposerButtons($('#mobile-text'), $('#mobile-send'), $('#mobile-paste'));
    toast('Input pasted', 'Text was appended without submitting.');
  };

  $('#keys-toggle').onclick = () => {
    const open = $('#softkeys').classList.toggle('open');
    $('#keys-toggle').setAttribute('aria-expanded', String(open));
  };
  $$('#softkeys button').forEach((button) => button.onclick = () => toast('Soft key sent', `${button.textContent} was sent to the terminal fixture.`));

  $('#rename-action').onclick = () => {
    const returnTarget = $('#session-options');
    hideMenus();
    generic('Rename session', `<label class="field">Display name<input id="rename-input" value="${esc(current().title)}"></label><button class="primary" id="save-name">Save name</button>`, (body) => {
      const input = $('#rename-input', body), save = $('#save-name', body);
      const validate = () => { save.disabled = !input.value.trim(); };
      input.oninput = validate; validate();
      save.onclick = () => {
        const name = input.value.trim();
        current().title = name;
        const row = $(`.session[data-branch="${CSS.escape(activeBranch)}"]`);
        row.dataset.title = name; $('.session-name', row).textContent = name;
        renderSession(); $('#generic-dialog').close(); toast('Session renamed', `Display name changed to ${name}.`);
      };
    }, returnTarget);
  };

  $('#color-action').onclick = () => {
    const returnTarget = $('#session-options');
    hideMenus();
    const colors = [['#3b82f6','Blue'],['#a855f7','Purple'],['#f97316','Orange'],['#ec4899','Pink']];
    generic('Session color',`<p>Session color is a personal identifier, not status or selection.</p><div id="color-choices" style="display:flex;gap:12px">${colors.map(([value,label]) => `<button class="touch" data-color="${value}" style="background:${value}" aria-label="${label}" aria-pressed="${String(current().color === value)}"></button>`).join('')}</div>`, (body) => {
      $$('[data-color]', body).forEach((button) => button.onclick = () => {
        $$('[data-color]', body).forEach((choice) => choice.setAttribute('aria-pressed','false'));
        button.setAttribute('aria-pressed','true');
        current().color = button.dataset.color;
        const row = $(`.session[data-branch="${CSS.escape(activeBranch)}"]`);
        let swatch = $('.swatch', row);
        if (!swatch) { swatch = document.createElement('i'); swatch.className = 'swatch'; row.append(swatch); }
        swatch.style.background = button.dataset.color;
        swatch.title = `Session color: ${button.getAttribute('aria-label').toLowerCase()}`;
        toast('Session color updated', `${button.getAttribute('aria-label')} identifies ${current().title}.`);
      });
    }, returnTarget);
  };

  $('#share-action').onclick = () => {
    const returnTarget = $('#session-options');
    hideMenus();
    const options = Object.values(fixtures).map((session) => `<option value="${esc(session.branch)}">${esc(session.title)}</option>`).join('');
    generic('Share target',`<p>Create a local preview of the existing target-selection step. Token execution remains production-only.</p><label class="field">Target session<input id="share-target" value="" list="share-targets" aria-describedby="share-result" autocomplete="off"><datalist id="share-targets">${options}</datalist></label><button class="primary" id="share-continue" disabled>Create share preview</button><p id="share-result" class="dim" aria-live="polite"></p>`, (body) => {
      const input = $('#share-target', body), button = $('#share-continue', body), result = $('#share-result', body);
      input.oninput = () => {
        button.disabled = !input.value.trim();
        input.removeAttribute('aria-invalid');
        result.textContent = '';
      };
      button.onclick = () => {
        const query = input.value.trim().toLowerCase();
        const target = Object.values(fixtures).find((session) => session.branch.toLowerCase() === query || session.title.toLowerCase() === query);
        if (!target) {
          input.setAttribute('aria-invalid','true');
          result.textContent = `No session fixture matches “${input.value.trim()}”. Choose an available session name or branch from the suggestions.`;
          input.focus();
          return;
        }
        input.removeAttribute('aria-invalid');
        result.textContent = `Validated target: ${target.title} (${target.branch}). Production would create the share token next.`;
        button.disabled = true;
        toast('Share target validated', `${target.title} (${target.branch})`);
      };
    }, returnTarget);
  };

  let deleteArmed = false;
  $('#delete-action').onclick = () => {
    if (!deleteArmed) {
      deleteArmed = true;
      $('#delete-action').textContent = `Confirm delete ${current().title}`;
      toast('Delete armed','Select Confirm within 3 seconds. Cleanup progress appears here.');
      setTimeout(() => { deleteArmed = false; $('#delete-action').textContent = 'Delete session…'; }, 3000);
    } else {
      deleteArmed = false; hideMenus(); toast('Removal preview started','Static fixture retained; production cleanup would run here.');
    }
  };

  $$('[data-close-dialog]').forEach((button) => button.onclick = () => button.closest('dialog').close());
  $$('dialog').forEach((dialog) => dialog.addEventListener('close', () => setTimeout(() => {
    const target = dialogReturns.get(dialog);
    if (target?.isConnected && !target.closest('[inert]') && target.offsetParent !== null) target.focus();
  }, 0)));

  $('#new-session').onclick = () => {
    $('#new-name').value = '';
    $('#create-session').disabled = true;
    openDialog($('#new-dialog'), $('#new-session'));
  };
  const validateNew = () => {
    const valid = $('#new-project').value.trim() && $('#new-name').value.trim();
    $('#create-session').disabled = !valid;
    $('#new-error').textContent = valid ? '' : 'Project and session name are required.';
  };
  $('#new-project').oninput = validateNew;
  $('#new-name').oninput = validateNew;
  $('#new-form').onsubmit = (event) => {
    event.preventDefault(); validateNew();
    if ($('#create-session').disabled) return;
    addCreatedSession($('#new-project').value, $('#new-name').value, $('#new-target').value);
    $('#new-dialog').close();
    toast('Session fixture created', `${current().title} was added and selected.`);
  };

  $('#stale-launch').onclick = () => openDialog($('#stale-dialog'), $('#stale-launch'));
  $('#state-gallery').onclick = () => openDialog($('#states-dialog'), $('#state-gallery'));
  $('#show-image-toast').onclick = () => { $('#states-dialog').close(); presentGalleryToast(showRemoteImageToast); };
  $('#show-flag-toast').onclick = () => { $('#states-dialog').close(); presentGalleryToast(showFlagToast); };
  $('#prune-action').onclick = (event) => {
    const confirming = event.currentTarget.textContent.startsWith('Confirm');
    event.currentTarget.textContent = confirming ? 'Pruning preview complete ✓' : 'Confirm prune 1 clean';
    if (confirming) event.currentTarget.disabled = true;
    toast('Stale cleanup', confirming ? 'The placement reached its completed preview state.' : 'Confirm once more to preview completion.');
  };
  $('#repair-action').onclick = () => { $('#stale-dialog').close(); selectSession('jf-mcp-audit', {feedback:true}); };
  $('#reviewed-action').onclick = (event) => { event.currentTarget.textContent = 'Reviewed ✓'; event.currentTarget.disabled = true; toast('Review recorded','The fixture now shows its reviewed state.'); };

  $('#quick-input').oninput = (event) => $$('.switch-item').forEach((item) => item.hidden = !item.textContent.toLowerCase().includes(event.target.value.toLowerCase()));
  $$('#quick-dialog .switch-item').forEach((button) => button.onclick = () => {
    $('#quick-dialog').close();
    selectSession(button.dataset.branch, {feedback:true});
  });
  function openQuick() {
    $('#quick-input').value = '';
    $$('#quick-dialog .switch-item').forEach((item) => item.hidden = false);
    openDialog($('#quick-dialog'), document.activeElement);
    setTimeout(() => $('#quick-input').focus(), 0);
  }

  $('#stage').addEventListener('dragenter', (event) => { event.preventDefault(); $('#stage').classList.add('dragging'); });
  $('#stage').addEventListener('dragover', (event) => event.preventDefault());
  $('#stage').addEventListener('dragleave', (event) => { if (!$('#stage').contains(event.relatedTarget)) $('#stage').classList.remove('dragging'); });
  $('#stage').addEventListener('drop', (event) => {
    event.preventDefault(); $('#stage').classList.remove('dragging');
    const file = [...event.dataTransfer.files].find((item) => item.type.startsWith('image/'));
    file ? toast('Image accepted', `${file.name} reached the static attachment confirmation.`) : toast('Image not accepted','Drop a PNG, JPG, GIF, or WebP image.');
  });
  document.addEventListener('paste', (event) => {
    if ([...(event.clipboardData?.items || [])].some((item) => item.type.startsWith('image/'))) toast('Pasted image','Image paste reached the static attachment confirmation.');
  });

  document.addEventListener('keydown', (event) => {
    if ((event.metaKey || event.ctrlKey) && !event.shiftKey && event.key.toLowerCase() === 'p') {
      event.preventDefault();
      $('#quick-dialog').open ? $('#quick-dialog').close() : openQuick();
      return;
    }
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
      event.preventDefault(); toggleCompose(); return;
    }
    if (event.key === '/' && !isEditable(event.target) && !$$('dialog[open]').length) {
      event.preventDefault();
      if (innerWidth < 1024) setNav(true);
      else search.focus();
      return;
    }
    if (event.key === 'Escape') {
      if ($$('dialog[open]').length) return;
      if (!sessionMenu.hidden || !actionsMenu.hidden) { event.preventDefault(); hideMenus({restore:true}); return; }
      if (nav.classList.contains('open')) { event.preventDefault(); setNav(false); return; }
      if (composer.classList.contains('open')) { event.preventDefault(); toggleCompose(false); return; }
      if (statusPanel.classList.contains('open')) { event.preventDefault(); closeStatus(true); if (isPhone()) setMobileActive('terminal'); return; }
      if (artifactPanel.classList.contains('open')) { event.preventDefault(); closeArtifacts(true); if (isPhone()) setMobileActive('terminal'); return; }
    }
    if (nav.classList.contains('open') && event.key === 'Tab') {
      const items = focusables(nav);
      if (!items.length) return;
      const first = items[0], last = items[items.length - 1];
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
    }
  });

  addEventListener('resize', () => {
    if (innerWidth >= 1024 && nav.classList.contains('open')) setNav(false, {restore:false});
    if (isCompact() && statusPanel.classList.contains('open') && artifactPanel.classList.contains('open')) closeArtifacts(false);
    syncNavigatorMode(); syncPanelIsolation(); syncResponsiveActions();
  });

  renderSession();
  syncNavigatorMode();
  syncPanelIsolation();
  syncResponsiveActions();
})();

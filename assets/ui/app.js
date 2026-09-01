'use strict';

// The whole client. No framework, no build step, no network origin but this
// one — the response CSP forbids anything else, and a maintainer's
// credential-holding localhost port is the last place a CDN belongs.

const $ = (sel) => document.querySelector(sel);

const state = {
  modules: [],
  filter: '',
  view: 'work',         // 'work' = contributions waiting on you; 'issues' = the whole queue
  open: new Set(),      // module names expanded; survives a refresh of the data
  job: null,            // { id, offset, timer }
};

async function api(path, options) {
  const response = await fetch(path, {
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    ...options,
  });
  if (!response.ok) {
    // A 404 on every route means the server no longer accepts this tab's
    // token — it was restarted, and a fresh one was minted. Reloading lands on
    // the page route, which explains that rather than repeating this.
    if (response.status === 404 && !path.startsWith('/api/jobs/')) {
      throw new Error('This tab is from a previous run of upkeep ui. Reload to see what to do.');
    }
    const body = await response.json().catch(() => ({ error: response.statusText }));
    throw new Error(body.error || 'Request failed');
  }
  return response.json();
}

// ---------------------------------------------------------------- rendering

function entriesFor(module) {
  return state.view === 'issues' ? module.issues : module.rows;
}

function matches(module, needle) {
  if (!needle) return true;
  if (module.module.includes(needle)) return true;
  return entriesFor(module).some((entry) =>
    String(entry.issue || entry.nid || '').includes(needle) ||
    String(entry.mr || '').includes(needle) ||
    (entry.title || '').toLowerCase().includes(needle));
}

function countsFor(module) {
  const s = module.summary;
  if (!s) return 'never fetched';
  if (s.failed) return 'unavailable';

  const parts = [];
  if (state.view === 'issues') {
    const issues = module.issues || [];
    const unclaimed = issues.filter((i) => i.unclaimed).length;
    const awaiting = issues.filter((i) => i.awaits_maintainer).length;
    if (issues.length) parts.push(`${issues.length} open`);
    if (awaiting) parts.push(`${awaiting} awaiting you`);
    if (unclaimed) parts.push(`${unclaimed} unclaimed`);
    return parts.length ? parts.join(' · ') : 'nothing open';
  }

  if (s.merge_requests) parts.push(`${s.merge_requests} MR${s.merge_requests === 1 ? '' : 's'}`);
  if (s.patch_issues) parts.push(`${s.patch_issues} patch`);
  if (s.ready_auto) parts.push(`${s.ready_auto} ready`);
  if (s.blocked) parts.push(`${s.blocked} blocked`);
  if (s.unchecked) parts.push(`${s.unchecked} unchecked`);
  return parts.length ? parts.join(' · ') : 'nothing open';
}

function rowElement(module, row) {
  const el = document.createElement('div');
  el.className = `row row-${row.kind}`;

  const subject = row.kind === 'patch' ? `patch #${row.issue}` : `!${row.mr}`;
  const link = row.url ? `<a href="${row.url}" target="_blank" rel="noreferrer noopener">${subject}</a>` : subject;

  el.innerHTML = `
    <span class="subject">${link}</span>
    <span class="core">${row.core}</span>
    <span class="title"></span>
    <span class="cell ci-${row.ci}">${row.ci}</span>
    <span class="cell local-${row.local}">${row.local}</span>
    <span class="status">${row.status}</span>`;
  // Titles are remote text; set as a text node so no issue title can ever be
  // markup on this page.
  el.querySelector('.title').textContent = row.title || '';

  const action = document.createElement('button');
  action.type = 'button';
  action.className = 'run';
  action.textContent = row.local === '–' || row.local === 'stale' ? 'Check' : 'Re-check';
  action.addEventListener('click', () => startJob(
    row.kind === 'patch'
      ? { action: 'patch-check', module: module.module, core: row.core, issue: String(row.issue) }
      : { action: 'check', module: module.module, core: row.core, mr: String(row.mr) },
  ));
  el.append(action);

  return el;
}

function issueElement(module, issue) {
  const el = document.createElement('div');
  el.className = `row row-issue${issue.unclaimed ? ' row-unclaimed' : ''}`;

  const contribution = issue.mr ? `!${issue.mr}` : (issue.patches ? `${issue.patches} patch` : 'unclaimed');
  el.innerHTML = `
    <span class="subject"><a href="${issue.url}" target="_blank" rel="noreferrer noopener">#${issue.nid}</a></span>
    <span class="cell status-${issue.status.replace(/\s+/g, '-')}">${issue.status}</span>
    <span class="cell">${issue.priority || '–'}</span>
    <span class="cell contribution">${contribution}</span>
    <span class="title"></span>`;
  // Titles are remote text; set as a text node so no issue title can ever be
  // markup on this page.
  el.querySelector('.title').textContent = issue.title || '';

  const actions = document.createElement('span');
  actions.className = 'actions';

  const start = document.createElement('button');
  start.type = 'button';
  start.className = 'run';
  start.textContent = 'Start';
  start.title = 'Provision an environment and open a work branch (resumes if one exists)';
  start.addEventListener('click', () => startJob({
    action: 'start', module: module.module, issue: String(issue.nid),
  }));
  actions.append(start);

  // The one control that reaches outside this machine, so it asks first: a
  // merge request is public the moment it exists.
  const publish = document.createElement('button');
  publish.type = 'button';
  publish.className = 'run publish';
  publish.textContent = 'Publish';
  publish.title = 'Push the work branch and open its merge request';
  publish.addEventListener('click', () => confirmPublish(module.module, issue));
  actions.append(publish);

  el.append(actions);

  return el;
}

function confirmPublish(module, issue) {
  const dialog = $('#confirm');
  $('#confirm-text').textContent =
    `Push the work branch for #${issue.nid} and open a merge request on drupal.org?`;

  const onClose = () => {
    dialog.removeEventListener('close', onClose);
    if (dialog.returnValue === 'ok') {
      startJob({ action: 'publish', module, issue: String(issue.nid) });
    }
  };
  dialog.addEventListener('close', onClose);
  dialog.showModal();
}

function moduleElement(module) {
  const node = $('#module-template').content.cloneNode(true);
  const section = node.querySelector('.module');
  const head = node.querySelector('.module-head');
  const rows = node.querySelector('.rows');
  const expanded = state.open.has(module.module);

  node.querySelector('.name').textContent = module.module;
  node.querySelector('.counts').textContent = countsFor(module);
  node.querySelector('.cached').textContent = module.cached ? `cached ${module.cached}` : '';
  node.querySelector('.chev').textContent = expanded ? '▾' : '▸';
  head.setAttribute('aria-expanded', String(expanded));
  rows.hidden = !expanded;

  head.addEventListener('click', () => {
    state.open.has(module.module) ? state.open.delete(module.module) : state.open.add(module.module);
    render();
  });

  // Rows are only built for an expanded module: a cockpit can hold thousands,
  // and the whole reason this UI exists is that rendering all of them at once
  // is what made the terminal unusable.
  if (expanded) {
    const entries = entriesFor(module);
    if (!entries.length) {
      const empty = document.createElement('p');
      empty.className = 'empty';
      empty.textContent = module.cached
        ? (state.view === 'issues' ? 'No open issues.' : 'Nothing waiting on you.')
        : 'Not fetched yet — refresh this module.';
      rows.append(empty);
    }
    entries.forEach((entry) => rows.append(
      state.view === 'issues' ? issueElement(module, entry) : rowElement(module, entry),
    ));

    const refresh = document.createElement('button');
    refresh.type = 'button';
    refresh.className = 'refresh';
    refresh.textContent = 'Refresh from drupal.org and GitLab';
    refresh.addEventListener('click', () => startJob({ action: 'refresh', module: module.module }));
    rows.append(refresh);
  }

  return section;
}

function render() {
  const needle = state.filter.trim().toLowerCase();
  const visible = state.modules.filter((m) => matches(m, needle));
  const main = $('#modules');
  main.textContent = '';

  if (!visible.length) {
    main.textContent = state.modules.length ? 'Nothing matches that filter.' : 'No modules registered.';
    return;
  }
  visible.forEach((module) => main.append(moduleElement(module)));
}

// -------------------------------------------------------------------- jobs

async function startJob(body) {
  try {
    const { job } = await api('/api/jobs', { method: 'POST', body: JSON.stringify(body) });
    openDrawer(job);
  } catch (error) {
    openDrawer({ id: null, label: 'Could not start', state: 'infrastructure' }, error.message);
  }
}

function openDrawer(job, message) {
  stopPolling();
  $('#drawer').hidden = false;
  $('#job-label').textContent = job.label;
  $('#job-state').textContent = job.state;
  $('#job-state').className = `state-${job.state}`;
  $('#job-output').textContent = message || '';
  if (!job.id) return;

  state.job = { id: job.id, offset: 0, timer: null };
  poll();
}

function stopPolling() {
  if (state.job && state.job.timer) clearTimeout(state.job.timer);
  state.job = null;
}

// Polling, not a held-open stream: the built-in server has few workers and one
// blocked request is most of them. The offset is the only thing the client has
// to remember, so a refresh resumes rather than restarts.
async function poll() {
  if (!state.job) return;
  const { id, offset } = state.job;

  try {
    const data = await api(`/api/jobs/${id}?offset=${offset}`);
    if (!state.job || state.job.id !== id) return;

    if (data.output) $('#job-output').textContent += data.output;
    $('#job-output').scrollTop = $('#job-output').scrollHeight;
    $('#job-state').textContent = data.job.state;
    $('#job-state').className = `state-${data.job.state}`;
    state.job.offset = data.offset;

    if (data.complete) {
      stopPolling();
      load();      // a finished job changes what the rows say
      return;
    }
  } catch (error) {
    $('#job-output').textContent += `\n${error.message}\n`;
    stopPolling();
    return;
  }

  state.job.timer = setTimeout(poll, 700);
}

// ------------------------------------------------------------------- boot

async function load() {
  try {
    const data = await api('/api/state');
    state.modules = data.modules.map((m) => ({ ...m, issues: m.issues || [] }));
    $('#meta').textContent = `${data.modules.length} module${data.modules.length === 1 ? '' : 's'}`;
    render();
  } catch (error) {
    $('#modules').textContent = error.message;
  }
}

$('#filter').addEventListener('input', (event) => {
  state.filter = event.target.value;
  render();
});

function setView(view) {
  state.view = view;
  $('#view-work').setAttribute('aria-selected', String(view === 'work'));
  $('#view-issues').setAttribute('aria-selected', String(view === 'issues'));
  render();
}
$('#view-work').addEventListener('click', () => setView('work'));
$('#view-issues').addEventListener('click', () => setView('issues'));
$('#drawer-close').addEventListener('click', () => {
  stopPolling();
  $('#drawer').hidden = true;
});

load();

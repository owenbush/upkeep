'use strict';

// The whole client. No framework, no build step, no network origin but this
// one — the response CSP forbids anything else, and a maintainer's
// credential-holding localhost port is the last place a CDN belongs.

const $ = (sel) => document.querySelector(sel);

const state = {
  modules: [],
  filter: '',
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
    const body = await response.json().catch(() => ({ error: response.statusText }));
    throw new Error(body.error || 'Request failed');
  }
  return response.json();
}

// ---------------------------------------------------------------- rendering

function matches(module, needle) {
  if (!needle) return true;
  if (module.module.includes(needle)) return true;
  return module.rows.some((row) =>
    String(row.issue || '').includes(needle) ||
    String(row.mr || '').includes(needle) ||
    (row.title || '').toLowerCase().includes(needle));
}

function countsFor(module) {
  const s = module.summary;
  if (!s) return 'never fetched';
  if (s.failed) return 'unavailable';
  const parts = [];
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
    if (!module.rows.length) {
      const empty = document.createElement('p');
      empty.className = 'empty';
      empty.textContent = module.cached ? 'Nothing open.' : 'Not fetched yet — refresh this module.';
      rows.append(empty);
    }
    module.rows.forEach((row) => rows.append(rowElement(module, row)));

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
    state.modules = data.modules;
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
$('#drawer-close').addEventListener('click', () => {
  stopPolling();
  $('#drawer').hidden = true;
});

// The token arrived in the URL and is now in a cookie; drop it from the address
// bar so it stops appearing in history and in screenshots.
if (location.search.includes('token=')) {
  history.replaceState(null, '', location.pathname);
}

load();

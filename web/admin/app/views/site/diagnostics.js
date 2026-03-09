/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

/**
 * Site-scoped diagnostics panel.
 */

import { h, clear } from '../../core/dom.js';
import { get } from '../../core/http.js';
import { icon } from '../../ui/icon.js';
import * as toast from '../../ui/toast.js';
import { emptyState } from '../../ui/helpers.js';

export async function renderSiteDiagnostics(container, siteId) {
  clear(container);

  const refreshBtn = h('button', {
    className: 'btn btn--sm btn--secondary',
    onClick: () => loadAll(body, siteId),
  }, 'Run Checks');

  const header = h('div', { className: 'context-panel__header' }, [
    h('h3', { className: 'context-panel__title' }, 'Diagnostics'),
    refreshBtn,
  ]);

  const body = h('div', { className: 'context-panel__body' });
  container.appendChild(header);
  container.appendChild(body);

  await loadAll(body, siteId);
}

async function loadAll(container, siteId) {
  clear(container);
  container.appendChild(h('p', { className: 'text-secondary text-sm', style: { padding: '12px' } }, 'Running checks...'));

  try {
    const [health, integrity, errors] = await Promise.all([
      get(`/admin/api/sites/${siteId}/diagnostics/health`),
      get(`/admin/api/sites/${siteId}/diagnostics/integrity`),
      get(`/admin/api/sites/${siteId}/diagnostics/errors`),
    ]);
    renderResults(container, health, integrity, errors);
  } catch (err) {
    clear(container);
    toast.error('Failed to run diagnostics: ' + err.message);
  }
}

function renderResults(container, health, integrity, errors) {
  clear(container);

  // Health section
  container.appendChild(sectionHeader('System Health', 'activity'));
  const rt = health.runtime || {};
  const mem = health.memory || {};
  const site = health.site || {};
  container.appendChild(h('div', { className: 'flex gap-3 mb-3', style: { flexWrap: 'wrap' } }, [
    miniCard('Go', rt.go_version),
    miniCard('Goroutines', rt.goroutines),
    miniCard('Memory', mem.alloc_mb + ' MB'),
    miniCard('GC Runs', mem.num_gc),
    miniCard('Pages', site.pages),
    miniCard('Memories', site.memories),
    miniCard('Tables', site.tables),
  ]));

  // Integrity section
  container.appendChild(sectionHeader('Integrity Check', integrity.ok ? 'check' : 'alert-circle'));
  const issues = integrity.issues || [];
  if (issues.length === 0) {
    container.appendChild(h('div', {
      className: 'text-sm mb-3',
      style: { padding: '8px 12px', background: 'var(--bg-secondary)', borderRadius: '6px', color: 'var(--success)' },
    }, 'All checks passed.'));
  } else {
    for (const issue of issues) {
      const isError = issue.level === 'error';
      container.appendChild(h('div', {
        className: `flex items-center gap-2 text-sm mb-2`,
        style: { padding: '6px 12px', background: 'var(--bg-secondary)', borderRadius: '4px' },
      }, [
        h('span', {
          innerHTML: icon(isError ? 'alert-circle' : 'alert-circle'),
          style: { color: isError ? 'var(--danger)' : 'var(--warning)' },
        }),
        h('span', {}, issue.message),
      ]));
    }
  }

  // Errors section
  container.appendChild(sectionHeader('Recent Errors', 'zap'));
  if (errors.length === 0) {
    container.appendChild(h('div', {
      className: 'text-sm mb-3',
      style: { padding: '8px 12px', background: 'var(--bg-secondary)', borderRadius: '6px' },
    }, 'No recent errors.'));
  } else {
    for (const err of errors) {
      container.appendChild(h('div', { className: 'card mb-2' }, [
        h('div', { className: 'card__header' }, [
          h('div', { className: 'flex items-center gap-2' }, [
            h('span', { className: 'badge badge--danger' }, err.event_type),
            h('span', { className: 'text-sm' }, err.summary),
          ]),
          h('span', { className: 'text-xs text-secondary' },
            new Date(err.created_at).toLocaleString()),
        ]),
        ...(err.details ? [h('div', {
          className: 'text-xs text-secondary',
          style: { padding: '0 12px 12px', whiteSpace: 'pre-wrap', wordBreak: 'break-word', maxHeight: '100px', overflow: 'auto' },
        }, err.details)] : []),
      ]));
    }
  }
}

function sectionHeader(title, iconName) {
  return h('h4', {
    className: 'flex items-center gap-2 text-sm text-secondary mb-2',
    style: { padding: '0 4px', marginTop: '12px' },
  }, [
    h('span', { innerHTML: icon(iconName) }),
    h('span', {}, title),
  ]);
}

function miniCard(label, value) {
  return h('div', {
    className: 'card',
    style: { padding: '8px 12px', minWidth: '80px', textAlign: 'center' },
  }, [
    h('div', { className: 'text-xs text-secondary' }, label),
    h('div', { style: { fontSize: '1.1rem', fontWeight: 600 } }, String(value ?? '-')),
  ]);
}

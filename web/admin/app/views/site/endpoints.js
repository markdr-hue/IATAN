/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

/**
 * API & Auth Endpoints manager view for context panel.
 */

import { h, clear } from '../../core/dom.js';
import { get, del } from '../../core/http.js';
import { icon } from '../../ui/icon.js';
import * as toast from '../../ui/toast.js';
import * as modal from '../../ui/modal.js';
import * as state from '../../core/state.js';
import { emptyState } from '../../ui/helpers.js';

export async function renderSiteEndpoints(container, siteId) {
  clear(container);

  const header = h('div', { className: 'context-panel__header' }, [
    h('h3', { className: 'context-panel__title' }, 'Endpoints'),
  ]);

  const listContainer = h('div', { className: 'context-panel__body' });
  container.appendChild(header);
  container.appendChild(listContainer);

  const unwatch = state.watch('toolExecuted', (evt) => {
    if (evt.site_id === siteId && evt.tool === 'manage_endpoints' && evt.success) {
      loadEndpoints(listContainer, siteId);
    }
  });

  await loadEndpoints(listContainer, siteId);
  return () => unwatch();
}

async function loadEndpoints(container, siteId) {
  try {
    const [apiEndpoints, authEndpoints] = await Promise.all([
      get(`/admin/api/sites/${siteId}/endpoints`),
      get(`/admin/api/sites/${siteId}/auth-endpoints`),
    ]);
    renderEndpointsList(container, apiEndpoints, authEndpoints, siteId);
  } catch (err) {
    toast.error('Failed to load endpoints: ' + err.message);
  }
}

function renderEndpointsList(container, apiEndpoints, authEndpoints, siteId) {
  clear(container);

  const hasApi = apiEndpoints && apiEndpoints.length > 0;
  const hasAuth = authEndpoints && authEndpoints.length > 0;

  if (!hasApi && !hasAuth) {
    container.appendChild(emptyState('No endpoints created yet.'));
    return;
  }

  // API Endpoints
  if (hasApi) {
    container.appendChild(h('h4', { className: 'text-sm text-secondary mb-2', style: { padding: '0 4px' } }, 'API Endpoints'));
    for (const ep of apiEndpoints) {
      container.appendChild(renderApiCard(ep, siteId, container));
    }
  }

  // Auth Endpoints
  if (hasAuth) {
    container.appendChild(h('h4', {
      className: 'text-sm text-secondary mb-2',
      style: { padding: '0 4px', marginTop: hasApi ? '16px' : '0' },
    }, 'Auth Endpoints'));
    for (const ep of authEndpoints) {
      container.appendChild(renderAuthCard(ep, siteId, container));
    }
  }
}

function renderApiCard(ep, siteId, container) {
  let methods = [];
  try { methods = JSON.parse(ep.methods || '[]'); } catch { methods = []; }

  return h('div', { className: 'card mb-3' }, [
    h('div', { className: 'card__header' }, [
      h('div', { className: 'flex items-center gap-2' }, [
        h('span', { innerHTML: icon('zap') }),
        h('code', { style: { fontSize: 'var(--text-sm)' } }, `/api/${ep.path}`),
      ]),
      h('button', {
        className: 'btn btn--ghost btn--sm',
        title: 'Delete endpoint',
        onClick: (e) => {
          e.stopPropagation();
          confirmDeleteApi(container, ep, siteId);
        },
      }, [h('span', { innerHTML: icon('trash') })]),
    ]),
    h('div', { style: { padding: '0 12px 12px' } }, [
      h('div', { className: 'flex items-center gap-2 mb-2' }, [
        h('span', { className: 'text-sm text-secondary' }, 'Table:'),
        h('span', { className: 'badge badge--info' }, ep.table_name),
      ]),
      h('div', { className: 'flex items-center gap-2 mb-2' },
        methods.map(m => h('span', { className: 'badge' }, m))
      ),
      h('div', { className: 'flex items-center gap-3 text-sm text-secondary' }, [
        ep.requires_auth
          ? h('span', { className: 'flex items-center gap-1' }, [
              h('span', { innerHTML: icon('lock') }), 'Auth required',
            ])
          : h('span', { className: 'flex items-center gap-1' }, [
              h('span', { innerHTML: icon('globe') }), 'Public',
            ]),
        h('span', {}, `${ep.rate_limit}/min`),
      ]),
    ]),
  ]);
}

function renderAuthCard(ep, siteId, container) {
  const routes = ep.routes || [];

  return h('div', { className: 'card mb-3' }, [
    h('div', { className: 'card__header' }, [
      h('div', { className: 'flex items-center gap-2' }, [
        h('span', { innerHTML: icon('key') }),
        h('code', { style: { fontSize: 'var(--text-sm)' } }, `/api/${ep.path}`),
        h('span', { className: 'badge badge--warning' }, 'Auth'),
      ]),
      h('button', {
        className: 'btn btn--ghost btn--sm',
        title: 'Delete auth endpoint',
        onClick: (e) => {
          e.stopPropagation();
          confirmDeleteAuth(container, ep, siteId);
        },
      }, [h('span', { innerHTML: icon('trash') })]),
    ]),
    h('div', { style: { padding: '0 12px 12px' } }, [
      h('div', { className: 'flex items-center gap-2 mb-2' }, [
        h('span', { className: 'text-sm text-secondary' }, 'Table:'),
        h('span', { className: 'badge badge--info' }, ep.table_name),
      ]),
      h('div', { className: 'mb-2' },
        routes.map(r => h('div', { className: 'text-sm', style: { fontFamily: 'var(--font-mono)', padding: '2px 0' } }, r))
      ),
      h('div', { className: 'flex items-center gap-3 text-sm text-secondary' }, [
        h('span', {}, `Login: ${ep.username_column}`),
      ]),
    ]),
  ]);
}

function confirmDeleteApi(container, endpoint, siteId) {
  modal.confirmDanger('Delete API Endpoint', `Delete "/api/${endpoint.path}"? This cannot be undone.`, async () => {
    try {
      await del(`/admin/api/sites/${siteId}/endpoints/${endpoint.id}`);
      toast.success('Endpoint deleted');
      loadEndpoints(container, siteId);
    } catch (err) {
      toast.error(err.message);
    }
  });
}

function confirmDeleteAuth(container, endpoint, siteId) {
  modal.confirmDanger('Delete Auth Endpoint', `Delete auth endpoint "/api/${endpoint.path}"? This cannot be undone.`, async () => {
    try {
      await del(`/admin/api/sites/${siteId}/auth-endpoints/${endpoint.id}`);
      toast.success('Auth endpoint deleted');
      loadEndpoints(container, siteId);
    } catch (err) {
      toast.error(err.message);
    }
  });
}

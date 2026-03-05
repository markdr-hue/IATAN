/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

/**
 * API Endpoints manager view for context panel.
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
    h('h3', { className: 'context-panel__title' }, 'API Endpoints'),
  ]);

  const listContainer = h('div', { className: 'context-panel__body' });
  container.appendChild(header);
  container.appendChild(listContainer);

  const unwatch = state.watch('toolExecuted', (evt) => {
    if (evt.site_id === siteId &&
        evt.tool === 'manage_endpoints' && ['create_api', 'delete_api', 'create_auth', 'delete_auth'].includes(evt.args?.action) &&
        evt.success) {
      loadEndpoints(listContainer, siteId);
    }
  });

  await loadEndpoints(listContainer, siteId);
  return () => unwatch();
}

async function loadEndpoints(container, siteId) {
  try {
    const endpoints = await get(`/admin/api/sites/${siteId}/endpoints`);
    renderEndpointsList(container, endpoints, siteId);
  } catch (err) {
    toast.error('Failed to load endpoints: ' + err.message);
  }
}

function renderEndpointsList(container, endpoints, siteId) {
  clear(container);

  if (!endpoints || endpoints.length === 0) {
    container.appendChild(emptyState('No API endpoints created yet.'));
    return;
  }

  for (const ep of endpoints) {
    let methods = [];
    try { methods = JSON.parse(ep.methods || '[]'); } catch { methods = []; }

    const card = h('div', { className: 'card mb-3' }, [
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
            confirmDelete(container, ep, siteId);
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
    container.appendChild(card);
  }
}

function confirmDelete(container, endpoint, siteId) {
  modal.show('Delete Endpoint',
    h('p', {}, `Delete "/api/${endpoint.path}"? This cannot be undone.`),
    [
      { label: 'Cancel', onClick: () => {} },
      {
        label: 'Delete',
        className: 'btn btn--danger',
        onClick: async () => {
          try {
            await del(`/admin/api/sites/${siteId}/endpoints/${endpoint.id}`);
            toast.success('Endpoint deleted');
            loadEndpoints(container, siteId);
          } catch (err) {
            toast.error(err.message);
            return false;
          }
        },
      },
    ]
  );
}

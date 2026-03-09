/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

/**
 * Site-scoped brain memory panel.
 */

import { h, clear } from '../../core/dom.js';
import { get, del } from '../../core/http.js';
import { icon } from '../../ui/icon.js';
import * as toast from '../../ui/toast.js';
import * as modal from '../../ui/modal.js';
import * as state from '../../core/state.js';
import { emptyState } from '../../ui/helpers.js';

export async function renderSiteMemory(container, siteId) {
  clear(container);

  const header = h('div', { className: 'context-panel__header' }, [
    h('h3', { className: 'context-panel__title' }, 'Brain Memory'),
  ]);

  const body = h('div', { className: 'context-panel__body' });
  container.appendChild(header);
  container.appendChild(body);

  const unwatch = state.watch('toolExecuted', (evt) => {
    if (evt.site_id === siteId &&
        evt.tool === 'manage_memory' && ['remember', 'forget'].includes(evt.args?.action) &&
        evt.success) {
      loadMemory(body, siteId);
    }
  });

  await loadMemory(body, siteId);
  return () => unwatch();
}

async function loadMemory(container, siteId) {
  try {
    const memories = await get(`/admin/api/sites/${siteId}/memory`);
    renderMemoryList(container, memories, siteId);
  } catch (err) {
    toast.error('Failed to load memory: ' + err.message);
  }
}

function renderMemoryList(container, memories, siteId) {
  clear(container);

  if (!memories || memories.length === 0) {
    container.appendChild(emptyState('No memories stored. The AI stores design decisions and context here.'));
    return;
  }

  // Group by category
  const groups = {};
  for (const m of memories) {
    const cat = m.category || 'uncategorized';
    if (!groups[cat]) groups[cat] = [];
    groups[cat].push(m);
  }

  for (const [category, items] of Object.entries(groups)) {
    container.appendChild(h('h4', {
      className: 'text-sm text-secondary mb-2',
      style: { padding: '0 4px', marginTop: '12px' },
    }, category));

    for (const m of items) {
      const truncated = m.value.length > 200;
      const valueText = truncated ? m.value.slice(0, 200) + '...' : m.value;

      const valueEl = h('div', {
        className: 'text-sm',
        style: { padding: '0 12px 8px', whiteSpace: 'pre-wrap', wordBreak: 'break-word' },
      }, valueText);

      let expanded = false;
      const toggleBtn = truncated ? h('button', {
        className: 'btn btn--ghost btn--sm text-xs',
        style: { marginLeft: '8px', marginBottom: '8px' },
        onClick: () => {
          expanded = !expanded;
          valueEl.textContent = expanded ? m.value : valueText;
          toggleBtn.textContent = expanded ? 'Show less' : 'Show more';
        },
      }, 'Show more') : null;

      const children = [
        h('div', { className: 'card__header' }, [
          h('div', { className: 'flex items-center gap-2' }, [
            h('span', { innerHTML: icon('brain') }),
            h('h4', { className: 'card__title' }, m.key),
          ]),
          h('button', {
            className: 'btn btn--ghost btn--sm',
            title: 'Delete memory',
            onClick: () => {
              modal.confirmDanger('Delete Memory', `Delete "${m.key}"? The AI will lose this context.`, async () => {
                try {
                  await del(`/admin/api/sites/${siteId}/memory/${m.id}`);
                  toast.success('Memory deleted');
                  loadMemory(container, siteId);
                } catch (err) {
                  toast.error(err.message);
                }
              });
            },
          }, [h('span', { innerHTML: icon('trash') })]),
        ]),
        valueEl,
      ];
      if (toggleBtn) children.push(toggleBtn);
      children.push(h('div', { className: 'text-xs text-secondary', style: { padding: '0 12px 12px' } },
        'Updated: ' + new Date(m.updated_at).toLocaleString()));

      container.appendChild(h('div', { className: 'card mb-3' }, children));
    }
  }
}

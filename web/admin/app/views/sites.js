/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

/**
 * Sites list view.
 */

import { h, clear } from '../core/dom.js';
import { get, post, del } from '../core/http.js';
import { navigate } from '../core/router.js';
import { icon } from '../ui/icon.js';
import * as toast from '../ui/toast.js';
import * as modal from '../ui/modal.js';
import * as state from '../core/state.js';
import { emptyState, formatPublicUrl } from '../ui/helpers.js';
import { createModelPicker } from '../ui/model-picker.js';

export async function renderSites(container) {
  clear(container);

  const headerChildren = [
    h('h3', { className: 'context-panel__title context-panel__title--page' }, 'Sites'),
  ];
  if (state.isAdmin()) {
    headerChildren.push(h('button', {
      className: 'btn btn--primary',
      onClick: () => showCreateModal(),
    }, [
      h('span', { innerHTML: icon('plus') }),
      'New Site',
    ]));
  }

  const header = h('div', { className: 'context-panel__header context-panel__header--page flex items-center justify-between' }, headerChildren);
  const listContainer = h('div', { className: 'context-panel__body context-panel__body--page' });

  container.appendChild(header);
  container.appendChild(listContainer);

  const PAGE_SIZE = 50;
  let allSites = [];
  let totalCount = 0;

  async function loadSites() {
    allSites = [];
    totalCount = 0;
    try {
      const result = await get(`/admin/api/sites?limit=${PAGE_SIZE}&offset=0`);
      allSites = result.items;
      totalCount = result.total;
      // Also update global state with full list for sidebar/dashboard
      state.set('sites', allSites);
      renderSitesList();
    } catch (err) {
      toast.error('Failed to load sites: ' + err.message);
    }
  }

  async function loadMore() {
    try {
      const result = await get(`/admin/api/sites?limit=${PAGE_SIZE}&offset=${allSites.length}`);
      allSites = allSites.concat(result.items);
      state.set('sites', allSites);
      renderSitesList();
    } catch (err) {
      toast.error('Failed to load more sites: ' + err.message);
    }
  }

  function renderSitesList() {
    clear(listContainer);

    if (allSites.length === 0) {
      listContainer.appendChild(emptyState('No sites yet. Create your first site to start building with AI.'));
      return;
    }

    const runningSites = state.get('runningSites') || [];

    const rows = allSites.map(site => {
      const isRunning = runningSites.includes(site.id);
      return h('tr', {}, [
        h('td', {}, [
          h('div', {
            className: 'flex items-center gap-2',
            style: { cursor: 'pointer' },
            onClick: () => navigate(`/sites/${site.id}/home`),
          }, [
            h('span', {
              className: `status-dot${isRunning ? ' status-dot--active' : ''}`,
            }),
            h('strong', {}, site.name),
          ]),
        ]),
        h('td', {}, (() => {
          const { url, label } = formatPublicUrl(site, state.get('systemStatus'));
          return h('a', {
            href: url,
            target: '_blank',
            rel: 'noopener',
            className: 'link',
            onClick: (e) => e.stopPropagation(),
          }, label);
        })()),
        h('td', {}, [
          h('span', {
            className: `badge ${isRunning ? 'badge--success' : 'badge--warning'}`,
          }, isRunning ? 'Running' : 'Stopped'),
        ]),
        h('td', {}, site.mode || 'building'),
        h('td', {}, [
          h('div', { className: 'table__actions' }, [
            h('button', {
              className: 'btn btn--ghost btn--sm',
              title: 'Open',
              onClick: () => navigate(`/sites/${site.id}/home`),
              innerHTML: icon('chevron-right'),
            }),
            ...(state.isAdmin() ? [h('button', {
              className: 'btn btn--ghost btn--sm',
              title: 'Delete',
              onClick: () => confirmDelete(site),
              innerHTML: icon('trash'),
            })] : []),
          ]),
        ]),
      ]);
    });

    const tableWrapper = h('div', { className: 'table-wrapper' }, [
      h('table', { className: 'table' }, [
        h('thead', {}, [
          h('tr', {}, [
            h('th', {}, 'Name'),
            h('th', {}, 'Domain'),
            h('th', {}, 'Status'),
            h('th', {}, 'Mode'),
            h('th', {}, 'Actions'),
          ]),
        ]),
        h('tbody', {}, rows),
      ]),
    ]);

    listContainer.appendChild(tableWrapper);

    if (allSites.length < totalCount) {
      listContainer.appendChild(
        h('div', { style: 'text-align:center;padding:1rem' }, [
          h('button', {
            className: 'btn btn--ghost',
            onClick: loadMore,
          }, `Load more (${allSites.length} of ${totalCount})`),
        ])
      );
    }
  }

  async function showCreateModal() {
    const nameInput = h('input', { className: 'input', placeholder: 'My Website' });
    const domainInput = h('input', { className: 'input', placeholder: 'example.com (optional)' });
    const directionInput = h('textarea', {
      className: 'input',
      placeholder: 'Describe what this site should be about...',
      rows: '3',
    });

    // Fetch provider catalog for model picker
    let providers = [];
    try {
      providers = await get('/admin/api/providers/catalog');
    } catch {
      // If catalog fails, modal still works but model picker will be empty
    }

    const picker = createModelPicker(providers);
    const { providerSelect, modelSelect } = picker;

    const content = h('div', {}, [
      h('div', { className: 'form-group' }, [
        h('label', {}, ['Name', h('span', { className: 'required' }, ' *')]),
        nameInput,
      ]),
      h('div', { className: 'form-group' }, [
        h('label', {}, 'Domain'),
        domainInput,
      ]),
      h('div', { className: 'form-group' }, [
        h('label', {}, 'Direction'),
        directionInput,
      ]),
      h('div', { className: 'form-group' }, [
        h('label', {}, ['Provider', h('span', { className: 'required' }, ' *')]),
        providerSelect,
      ]),
      h('div', { className: 'form-group' }, [
        h('label', {}, ['Model', h('span', { className: 'required' }, ' *')]),
        modelSelect,
      ]),
    ]);

    modal.show('Create Site', content, [
      { label: 'Cancel', onClick: () => {} },
      {
        label: 'Create',
        className: 'btn btn--primary',
        onClick: async () => {
          const name = nameInput.value.trim();
          if (!name) {
            toast.warning('Site name is required');
            return false;
          }
          const selectedModel = picker.getSelectedModel();
          if (!selectedModel) {
            toast.warning('Please select a model');
            return false;
          }
          try {
            const site = await post('/admin/api/sites', {
              name,
              domain: domainInput.value.trim() || null,
              direction: directionInput.value.trim() || null,
              llm_model_id: selectedModel.id,
            });
            toast.success('Site created');
            navigate(`/sites/${site.id}/home`);
          } catch (err) {
            toast.error('Failed to create site: ' + err.message);
            return false;
          }
        },
      },
    ]);

    nameInput.focus();
  }

  function confirmDelete(site) {
    modal.confirmDanger(
      'Delete Site',
      `Are you sure you want to delete "${site.name}"? This action cannot be undone.`,
      async () => {
        try {
          await del(`/admin/api/sites/${site.id}`);
          toast.success('Site deleted');
          loadSites();
        } catch (err) {
          toast.error('Failed to delete site: ' + err.message);
        }
      }
    );
  }

  loadSites();
}

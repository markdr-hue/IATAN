/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

/**
 * Site settings tab - name, domain, mode, prompts, model, danger zone.
 */

import { h, clear } from '../../core/dom.js';
import { get, post, put, del } from '../../core/http.js';
import { navigate } from '../../core/router.js';
import { icon } from '../../ui/icon.js';
import * as toast from '../../ui/toast.js';
import * as modal from '../../ui/modal.js';
import * as state from '../../core/state.js';

export async function renderSiteSettings(container, siteId, site) {
  clear(container);

  const readOnly = !state.isAdmin();
  const nameInput = h('input', { className: 'input', value: site.name, disabled: readOnly });
  const domainInput = h('input', { className: 'input', value: site.domain || '', placeholder: 'example.com', disabled: readOnly });
  const descInput = h('textarea', {
    className: 'input',
    rows: '3',
    value: site.description || '',
    placeholder: 'Brief description of this site',
    disabled: readOnly,
  });
  const directionInput = h('textarea', {
    className: 'input',
    rows: '4',
    value: site.direction || '',
    placeholder: 'Describe what the AI should build and how it should behave...',
    disabled: readOnly,
  });

  // Model picker
  let providers = [];
  try {
    providers = await get('/admin/api/providers/catalog');
  } catch {
    // ignore
  }

  let selectedProvider = null;
  let selectedModel = null;

  const providerSelect = h('select', { className: 'input', disabled: readOnly });
  const modelSelect = h('select', { className: 'input', disabled: readOnly });

  providers.forEach((p) => {
    const opt = h('option', { value: String(p.id) }, p.name);
    providerSelect.appendChild(opt);
  });

  function updateModels() {
    modelSelect.innerHTML = '';
    selectedModel = null;
    if (!selectedProvider || !selectedProvider.models) return;

    selectedProvider.models.forEach((m) => {
      const opt = h('option', { value: String(m.id) }, m.display_name);
      if (m.id === site.llm_model_id) {
        opt.selected = true;
        selectedModel = m;
      }
      modelSelect.appendChild(opt);
    });

    if (!selectedModel && selectedProvider.models.length > 0) {
      selectedModel = selectedProvider.models[0];
      modelSelect.options[0].selected = true;
    }
  }

  for (const p of providers) {
    if (p.models && p.models.some(m => m.id === site.llm_model_id)) {
      selectedProvider = p;
      break;
    }
  }
  if (!selectedProvider && providers.length > 0) {
    selectedProvider = providers[0];
  }
  if (selectedProvider) {
    providerSelect.value = String(selectedProvider.id);
  }

  providerSelect.addEventListener('change', () => {
    const id = parseInt(providerSelect.value, 10);
    selectedProvider = providers.find(p => p.id === id) || null;
    updateModels();
  });

  modelSelect.addEventListener('change', () => {
    if (!selectedProvider) return;
    const mid = parseInt(modelSelect.value, 10);
    selectedModel = selectedProvider.models.find(m => m.id === mid) || null;
  });

  updateModels();

  const saveBtn = h('button', {
    className: 'btn btn--primary',
    onClick: async () => {
      if (!selectedModel) {
        toast.warning('Please select a model');
        return;
      }
      saveBtn.disabled = true;
      saveBtn.textContent = 'Saving...';
      try {
        await put(`/admin/api/sites/${siteId}`, {
          name: nameInput.value.trim(),
          domain: domainInput.value.trim() || null,
          description: descInput.value.trim() || null,
          direction: directionInput.value.trim() || null,
          llm_model_id: selectedModel.id,
        });
        toast.success('Site settings saved');

        const sites = await get('/admin/api/sites');
        state.set('sites', sites);
      } catch (err) {
        toast.error('Failed to save: ' + err.message);
      }
      saveBtn.disabled = false;
      saveBtn.textContent = 'Save Changes';
    },
  }, 'Save Changes');

  const header = h('div', { className: 'context-panel__header' }, [
    h('h3', { className: 'context-panel__title' }, 'Settings'),
  ]);

  const body = h('div', { className: 'context-panel__body' }, [
    h('div', { className: 'card' }, [
      h('h4', { className: 'card__title mb-4' }, 'General'),
      h('div', { className: 'form-group' }, [
        h('label', {}, ['Site Name', h('span', { className: 'required' }, ' *')]),
        nameInput,
      ]),
      h('div', { className: 'form-group' }, [
        h('label', {}, 'Domain'),
        domainInput,
        h('p', { className: 'form-hint' }, 'The domain this site is served on'),
      ]),
      h('div', { className: 'form-group' }, [
        h('label', {}, 'Description'),
        descInput,
      ]),
      h('div', { className: 'form-group' }, [
        h('label', {}, 'AI Direction'),
        directionInput,
        h('p', { className: 'form-hint' }, 'Instructions for how the AI should build and manage this site'),
      ]),
      h('div', { className: 'form-group' }, [
        h('label', {}, ['Provider', h('span', { className: 'required' }, ' *')]),
        providerSelect,
      ]),
      h('div', { className: 'form-group' }, [
        h('label', {}, ['Model', h('span', { className: 'required' }, ' *')]),
        modelSelect,
      ]),
      ...(state.isAdmin() ? [h('div', { className: 'mt-4' }, [saveBtn])] : []),
    ]),

    // Site visibility (admin only)
    ...(state.isAdmin() ? [h('div', { className: 'card' }, (() => {
      const isActive = site.status === 'active';
      const statusBadge = h('span', {
        className: `badge ${isActive ? 'badge--success' : 'badge--secondary'}`,
      }, isActive ? 'Live' : 'Disabled');

      const toggleBtn = h('button', {
        className: `btn ${isActive ? 'btn--danger' : 'btn--success'} btn--sm`,
        onClick: async () => {
          toggleBtn.disabled = true;
          try {
            const updated = await post(`/admin/api/sites/${siteId}/toggle-status`);
            toast.success(updated.status === 'active' ? 'Site is now live' : 'Site disabled');
            // Re-render with updated site data
            const sites = await get('/admin/api/sites');
            state.set('sites', sites);
            renderSiteSettings(container, siteId, updated);
          } catch (err) {
            toast.error('Failed to toggle: ' + err.message);
            toggleBtn.disabled = false;
          }
        },
      }, isActive ? 'Disable Site' : 'Enable Site');

      return [
        h('div', { className: 'card__header' }, [
          h('h4', { className: 'card__title' }, 'Site Visibility'),
          statusBadge,
        ]),
        h('p', { className: 'form-hint mt-2' },
          'Disabled sites return 404 to all visitors. The brain continues running independently.'
        ),
        h('div', { className: 'mt-3' }, [toggleBtn]),
      ];
    })())] : []),

    // Danger zone (admin only)
    ...(state.isAdmin() ? [h('div', { className: 'danger-zone' }, [
      h('h4', { className: 'danger-zone__title' }, 'Danger Zone'),
      h('p', { className: 'danger-zone__desc' },
        'Deleting a site will permanently remove all its data, pages, and chat history.'
      ),
      h('button', {
        className: 'btn btn--danger',
        onClick: () => {
          modal.confirmDanger(
            'Delete Site',
            `Are you sure you want to delete "${site.name}"? This action cannot be undone.`,
            async () => {
              try {
                await del(`/admin/api/sites/${siteId}`);
                toast.success('Site deleted');
                navigate('/sites');
              } catch (err) {
                toast.error('Failed to delete: ' + err.message);
              }
            }
          );
        },
      }, [
        h('span', { innerHTML: icon('trash') }),
        'Delete Site',
      ]),
    ])] : []),
  ]);

  container.appendChild(header);
  container.appendChild(body);
}

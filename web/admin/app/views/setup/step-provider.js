/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

/**
 * Setup Step 3: Connect AI provider.
 * Fetches providers from the database (seeded by firstrun.json) and presents
 * provider dropdown -> model dropdown -> API key (if required).
 * An AI provider is required — there is no skip option.
 */

import { h, clear } from '../../core/dom.js';
import { get } from '../../core/http.js';
import { icon } from '../../ui/icon.js';
import * as toast from '../../ui/toast.js';

export async function renderProvider(container, setupData, onNext) {
  // Show loading state while fetching providers
  const loading = h('div', { className: 'setup-card__header' }, [
    h('div', { className: 'setup-card__icon', innerHTML: icon('cpu') }),
    h('h2', { className: 'setup-card__title' }, 'Choose your AI'),
    h('p', { className: 'setup-card__desc' }, 'Loading providers...'),
    h('div', { style: 'text-align:center;padding:2rem' }, [
      h('span', { className: 'spinner' }),
    ]),
  ]);
  container.appendChild(loading);

  let providers = [];
  try {
    providers = await get('/admin/api/setup/providers');
  } catch {
    clear(container);
    container.appendChild(h('div', {}, [
      h('p', { style: 'color:var(--danger)' }, 'Failed to load providers.'),
    ]));
    return;
  }

  clear(container);

  if (!providers || providers.length === 0) {
    const content = h('div', {}, [
      h('div', { className: 'setup-card__header' }, [
        h('div', { className: 'setup-card__icon', innerHTML: icon('cpu') }),
        h('h2', { className: 'setup-card__title' }, 'No providers found'),
        h('p', { className: 'setup-card__desc' },
          'No AI providers were configured. You can add them later from Global Settings.'),
      ]),
      h('div', { className: 'setup-actions setup-actions--center' }, [
        h('button', {
          className: 'btn btn--primary btn--lg',
          onClick: () => {
            setupData.providerId = 0;
            setupData.providerName = '';
            setupData.modelId = '';
            setupData.apiKey = '';
            onNext();
          },
        }, 'Skip for now'),
      ]),
      h('p', { className: 'form-hint', style: 'text-align:center;margin-top:0.5rem' },
        'You can configure providers in Global Settings after setup.'),
    ]);
    container.appendChild(content);
    return;
  }

  buildForm(container, providers, setupData, onNext);
}

function buildForm(container, providers, setupData, onNext) {
  let selectedProvider = providers[0];
  let selectedModel = null;

  // -- Provider dropdown --
  const providerSelect = h('select', { className: 'input' });
  providers.forEach((p, i) => {
    const opt = h('option', { value: String(p.id) }, p.name);
    if (i === 0) opt.selected = true;
    providerSelect.appendChild(opt);
  });

  // -- Model dropdown --
  const modelSelect = h('select', { className: 'input' });

  // -- API key input --
  const apiKeyGroup = h('div', { className: 'form-group' });
  const apiKeyInput = h('input', {
    className: 'input',
    type: 'password',
    placeholder: 'Paste your API key',
  });
  const apiKeyHint = h('p', { className: 'form-hint' }, 'Your key stays on your server. We never share it.');

  function updateModels() {
    modelSelect.innerHTML = '';
    selectedModel = null;
    if (!selectedProvider || !selectedProvider.models) return;

    selectedProvider.models.forEach((m) => {
      const opt = h('option', { value: m.model_id }, m.display_name);
      if (m.is_default) {
        opt.selected = true;
        selectedModel = m;
      }
      modelSelect.appendChild(opt);
    });

    // If no default was set, select the first
    if (!selectedModel && selectedProvider.models.length > 0) {
      selectedModel = selectedProvider.models[0];
      modelSelect.options[0].selected = true;
    }
  }

  function updateAPIKeyVisibility() {
    apiKeyGroup.innerHTML = '';
    if (!selectedProvider) return;

    if (selectedProvider.requires_api_key && !selectedProvider.has_api_key) {
      apiKeyGroup.appendChild(h('label', {}, ['API Key', h('span', { className: 'required' }, ' *')]));
      apiKeyGroup.appendChild(apiKeyInput);
      apiKeyGroup.appendChild(apiKeyHint);
    } else if (selectedProvider.has_api_key) {
      apiKeyGroup.appendChild(
        h('p', { className: 'form-hint', style: 'color:var(--success)' },
          '\u2713 API key configured from environment.')
      );
    } else {
      apiKeyGroup.appendChild(
        h('p', { className: 'form-hint', style: 'color:var(--success)' },
          '\u2713 No API key needed for this provider.')
      );
    }
  }

  // Wire up change events
  providerSelect.addEventListener('change', () => {
    const id = parseInt(providerSelect.value, 10);
    selectedProvider = providers.find(p => p.id === id) || null;
    selectedModel = null;
    apiKeyInput.value = '';
    updateModels();
    updateAPIKeyVisibility();
  });

  modelSelect.addEventListener('change', () => {
    if (!selectedProvider) return;
    const mid = modelSelect.value;
    selectedModel = selectedProvider.models.find(m => m.model_id === mid) || null;
  });

  // Initialize
  updateModels();
  updateAPIKeyVisibility();

  function submit() {
    if (!selectedProvider) {
      toast.warning('Please select a provider');
      return;
    }
    const needsKey = selectedProvider.requires_api_key && !selectedProvider.has_api_key;
    if (needsKey && !apiKeyInput.value.trim()) {
      toast.warning('API key is required for ' + selectedProvider.name);
      apiKeyInput.focus();
      return;
    }
    setupData.providerId = selectedProvider.id;
    setupData.providerName = selectedProvider.name;
    setupData.modelId = selectedModel ? selectedModel.model_id : '';
    setupData.llmModelId = selectedModel ? selectedModel.id : 0;
    setupData.apiKey = needsKey ? apiKeyInput.value.trim() : '';
    onNext();
  }

  // Enter key on API key input submits
  apiKeyInput.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      submit();
    }
  });

  const content = h('div', {}, [
    h('div', { className: 'setup-card__header' }, [
      h('div', { className: 'setup-card__icon', innerHTML: icon('cpu') }),
      h('h2', { className: 'setup-card__title' }, 'Choose your AI'),
      h('p', { className: 'setup-card__desc' }, 'Pick the AI that will build your site. Don\u2019t worry \u2014 you can switch anytime.'),
    ]),
    h('div', { className: 'form-group' }, [
      h('label', {}, 'AI Service'),
      providerSelect,
    ]),
    h('div', { className: 'form-group' }, [
      h('label', {}, 'Model'),
      modelSelect,
    ]),
    apiKeyGroup,
    h('div', { className: 'setup-actions setup-actions--center' }, [
      h('button', {
        className: 'btn btn--primary btn--lg',
        onClick: submit,
      }, 'Continue'),
    ]),
    h('p', { className: 'setup-hint' }, 'Press Enter to continue'),
  ]);

  container.appendChild(content);
}

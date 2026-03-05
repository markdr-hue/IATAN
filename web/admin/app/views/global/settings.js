/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

/**
 * System settings editor.
 */

import { h, clear } from '../../core/dom.js';
import { get, put } from '../../core/http.js';
import * as toast from '../../ui/toast.js';
import * as state from '../../core/state.js';

export async function renderSettings(container) {
  clear(container);

  const header = h('div', { className: 'context-panel__header context-panel__header--page' }, [
    h('h3', { className: 'context-panel__title context-panel__title--page' }, 'System Settings'),
  ]);

  const formContainer = h('div', { className: 'context-panel__body context-panel__body--page' });

  container.appendChild(header);
  container.appendChild(formContainer);

  try {
    const settings = await get('/admin/api/settings');
    renderForm(settings);
  } catch (err) {
    toast.error('Failed to load settings: ' + err.message);
  }

  function renderForm(settings) {
    clear(formContainer);
    const readOnly = !state.isAdmin();

    // --- Known settings with proper UI ---
    const promptValue = settings['default_system_prompt'] || '';
    const promptInput = h('textarea', {
      className: 'input',
      rows: '10',
      value: promptValue,
      placeholder: 'Instructions that define how the AI chat assistant behaves...',
      disabled: readOnly,
    });

    const knownCard = h('div', { className: 'card' }, [
      h('h4', { className: 'card__title mb-4' }, 'Chat AI'),
      h('div', { className: 'form-group' }, [
        h('label', {}, 'Default System Prompt'),
        promptInput,
        h('p', { className: 'form-hint' },
          'Base instructions for chat sessions. The brain pipeline uses its own per-stage prompts and is not affected by this setting.'
        ),
      ]),
    ]);

    // --- Other settings (dynamic key-value) ---
    const otherFields = {};
    const otherKeys = Object.keys(settings).filter(k => k !== 'default_system_prompt');

    let otherCard = null;
    if (otherKeys.length > 0) {
      otherCard = h('div', { className: 'card mt-6' }, [
        h('h4', { className: 'card__title mb-4' }, 'Other'),
      ]);
      for (const key of otherKeys) {
        const input = h('input', {
          className: 'input',
          value: settings[key] || '',
          disabled: readOnly,
        });
        otherFields[key] = input;
        otherCard.appendChild(
          h('div', { className: 'form-group' }, [
            h('label', {}, key),
            input,
          ])
        );
      }
    }

    // --- Save button ---
    const saveBtn = h('button', {
      className: 'btn btn--primary',
      onClick: async () => {
        saveBtn.disabled = true;
        saveBtn.textContent = 'Saving...';
        try {
          const payload = {};
          const promptVal = promptInput.value.trim();
          if (promptVal) payload['default_system_prompt'] = promptVal;
          for (const [key, input] of Object.entries(otherFields)) {
            const val = input.value.trim();
            if (val) payload[key] = val;
          }
          await put('/admin/api/settings', payload);
          toast.success('Settings saved');
        } catch (err) {
          toast.error('Failed to save: ' + err.message);
        }
        saveBtn.disabled = false;
        saveBtn.textContent = 'Save Settings';
      },
    }, 'Save Settings');

    formContainer.appendChild(knownCard);
    if (otherCard) formContainer.appendChild(otherCard);

    if (state.isAdmin()) {
      formContainer.appendChild(h('div', { className: 'mt-4' }, [saveBtn]));
    }
  }
}

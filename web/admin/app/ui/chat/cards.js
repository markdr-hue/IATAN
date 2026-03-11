/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

/**
 * Interactive chat card factory.
 * Creates rich cards for specific tool types instead of generic "Tool: X → Done" cards.
 */

import { h } from '../../core/dom.js';
import { post } from '../../core/http.js';
import { icon } from '../icon.js';
import * as toast from '../toast.js';

// Navigation callback - set by site/index.js to control context panel
let _switchPanel = null;

export function setPanelSwitcher(fn) {
  _switchPanel = fn;
}

function switchPanel(panel) {
  if (_switchPanel) _switchPanel(panel);
}

/**
 * Route a tool call to the appropriate card renderer.
 * Returns { element, updateStatus } like createToolCall.
 */
// Action-level labels for manager tools: { toolName: { action: label } }
const managerLabels = {
  manage_pages: {
    save: 'Saving page', get: 'Reading page', list: 'Listing pages',
    delete: 'Deleting page', restore: 'Restoring page', history: 'Checking page history', search: 'Searching pages',
  },
  manage_files: {
    save: 'Saving file', get: 'Reading file', list: 'Listing files',
    delete: 'Deleting file', rename: 'Renaming file',
  },
  manage_schema: {
    create: 'Creating table', alter: 'Altering table', describe: 'Describing table',
    list: 'Listing tables', drop: 'Dropping table',
  },
  manage_data: {
    insert: 'Adding data', query: 'Querying data', update: 'Updating data',
    delete: 'Deleting data', count: 'Counting rows',
  },
  manage_endpoints: {
    create_api: 'Creating endpoint', list_api: 'Listing endpoints', delete_api: 'Removing endpoint',
    create_auth: 'Creating auth endpoint', list_auth: 'Listing auth endpoints', delete_auth: 'Removing auth endpoint',
    verify_password: 'Verifying password',
  },

  manage_communication: {
    ask: 'Asking a question', check: 'Checking answers',
  },
  manage_analytics: {
    query: 'Checking analytics', summary: 'Reading analytics summary',
  },
  manage_diagnostics: {
    health: 'Checking system health', errors: 'Reviewing errors', integrity: 'Checking integrity',
  },
  manage_webhooks: {
    create: 'Creating webhook', get: 'Reading webhook', list: 'Listing webhooks',
    delete: 'Removing webhook', update: 'Updating webhook', subscribe: 'Subscribing webhook',
  },
  manage_providers: {
    add: 'Adding service provider', list: 'Listing service providers',
    remove: 'Removing service provider', update: 'Updating service provider', request: 'Calling external API',
  },
  manage_secrets: {
    store: 'Storing secret', list: 'Listing secrets', delete: 'Removing secret',
  },
  manage_site: {
    info: 'Getting site info', set_mode: 'Changing site mode',
  },
  manage_scheduler: {
    create: 'Scheduling task', list: 'Listing tasks', update: 'Updating task', delete: 'Removing task',
  },
  manage_layout: {
    save: 'Saving layout', get: 'Reading layout', list: 'Listing layouts',
  },
};

// Fallback labels when no action is available.
const toolLabels = {
  manage_pages: 'Managing pages',
  manage_files: 'Managing files',
  manage_schema: 'Managing schema',
  manage_data: 'Managing data',
  manage_endpoints: 'Managing endpoints',

  manage_communication: 'Communication',
  manage_analytics: 'Checking analytics',
  manage_diagnostics: 'Running diagnostics',
  manage_webhooks: 'Managing webhooks',
  manage_providers: 'Managing providers',
  manage_secrets: 'Managing secrets',
  manage_site: 'Managing site',
  manage_scheduler: 'Managing scheduler',
  manage_layout: 'Managing layout',
  make_http_request: 'Making HTTP request',
};

/**
 * Get a friendly label for a tool, optionally using the action from args.
 */
export function getToolLabel(toolName, args) {
  if (args?.action && managerLabels[toolName]?.[args.action]) {
    return managerLabels[toolName][args.action];
  }
  return toolLabels[toolName] || toolName.replace(/_/g, ' ');
}

export function createToolCard(toolName, status, args, result) {
  const action = args?.action;

  switch (toolName) {
    case 'manage_pages':
      if (action === 'save' || action === 'delete' || action === 'restore') {
        return createPageCard(toolName, status, args, result);
      }
      return null;

    case 'manage_files':
      if (action === 'save' || action === 'delete') {
        return createAssetCard(toolName, status, args, result);
      }
      return null;

    case 'manage_schema':
    case 'manage_data':
      return createTableCard(toolName, status, args, result);

    case 'manage_endpoints':
      return createEndpointCard(toolName, status, args, result);

    default:
      return null; // Fallback to generic tool card
  }
}

/**
 * Create an inline question card with interactive option buttons.
 */
export function createQuestionCard(questionData) {
  const { id, question, options, urgency, context, fields } = questionData;

  // Parse structured fields if present.
  let parsedFields = [];
  if (fields) {
    try {
      parsedFields = typeof fields === 'string' ? JSON.parse(fields) : fields;
    } catch { /* ignore */ }
  }
  if (!Array.isArray(parsedFields)) parsedFields = [];

  let parsedOptions = [];
  if (options) {
    try {
      parsedOptions = typeof options === 'string' ? JSON.parse(options) : options;
    } catch { /* ignore */ }
  }
  if (!Array.isArray(parsedOptions)) parsedOptions = [];

  const card = h('div', { className: 'chat-card chat-card--question' });

  const header = h('div', { className: 'chat-card__header chat-card__header--question' }, [
    h('span', { innerHTML: icon('help-circle'), className: 'chat-card__icon chat-card__icon--question' }),
    h('span', { className: 'chat-card__label' }, 'Question'),
    urgency === 'high'
      ? h('span', { className: 'badge badge--danger' }, 'Urgent')
      : h('span', { className: 'badge badge--question' }, 'Needs your input'),
  ]);

  const body = h('div', { className: 'chat-card__body' }, [
    h('p', { className: 'chat-card__text' }, question),
  ]);

  if (context) {
    body.appendChild(h('p', { className: 'chat-card__context text-sm text-secondary' }, context));
  }

  card.appendChild(header);
  card.appendChild(body);

  // Multi-field structured input form.
  if (parsedFields.length > 0) {
    const fieldsContainer = h('div', { className: 'chat-card__fields' });
    const fieldInputs = {};

    for (const field of parsedFields) {
      const inputType = field.type === 'secret' ? 'password' : 'text';
      const input = h('input', {
        type: inputType,
        className: 'input input--sm',
        placeholder: field.label || field.name,
        'data-field-name': field.name,
      });
      fieldInputs[field.name] = input;

      const row = h('div', { className: 'chat-card__field-row' }, [
        h('label', { className: 'chat-card__field-label' }, field.label || field.name),
        input,
      ]);
      fieldsContainer.appendChild(row);
    }

    const submitBtn = h('button', {
      className: 'btn btn--sm btn--primary',
      onClick: () => {
        const values = {};
        let hasValue = false;
        for (const field of parsedFields) {
          const val = fieldInputs[field.name].value.trim();
          if (val) hasValue = true;
          values[field.name] = val;
        }
        if (hasValue) {
          submitAnswer(id, JSON.stringify(values), card);
        }
      },
    }, 'Submit');

    fieldsContainer.appendChild(h('div', { className: 'chat-card__field-actions' }, [submitBtn]));
    card.appendChild(fieldsContainer);
  } else {
    // Option buttons (original path)
    const optionsContainer = h('div', { className: 'chat-card__options' });
    for (const opt of parsedOptions) {
      const label = typeof opt === 'string' ? opt : opt.label || opt;
      const btn = h('button', {
        className: 'btn btn--sm btn--ghost chat-card__option-btn',
        onClick: () => submitAnswer(id, label, card),
      }, label);
      optionsContainer.appendChild(btn);
    }

    // Custom answer input
    const customInput = h('input', {
      type: 'text',
      className: 'input input--sm',
      placeholder: 'Or type a custom answer...',
      onKeyDown: (e) => {
        if (e.key === 'Enter' && customInput.value.trim()) {
          submitAnswer(id, customInput.value.trim(), card);
        }
      },
    });

    const customRow = h('div', { className: 'chat-card__custom-answer' }, [
      customInput,
      h('button', {
        className: 'btn btn--sm btn--primary',
        onClick: () => {
          if (customInput.value.trim()) {
            submitAnswer(id, customInput.value.trim(), card);
          }
        },
      }, 'Send'),
    ]);

    if (parsedOptions.length > 0) card.appendChild(optionsContainer);
    card.appendChild(customRow);
  }

  return { element: card, questionId: id };
}

async function submitAnswer(questionId, answer, cardEl) {
  try {
    await post(`/admin/api/questions/${questionId}/answer`, { answer });

    // Transform card to answered state
    cardEl.innerHTML = '';
    cardEl.className = 'chat-card chat-card--question chat-card--answered';

    cardEl.appendChild(h('div', { className: 'chat-card__header' }, [
      h('span', { innerHTML: icon('check'), className: 'chat-card__icon chat-card__icon--success' }),
      h('span', { className: 'chat-card__label' }, 'Answered'),
    ]));

    cardEl.appendChild(h('div', { className: 'chat-card__body' }, [
      h('p', { className: 'chat-card__answer-text' }, answer),
      h('button', {
        className: 'btn btn--ghost btn--sm mt-2',
        onClick: () => switchPanel('questions'),
      }, 'View all questions \u2192'),
    ]));

    // Notify chat view so it can show the answer bubble and update the banner
    document.dispatchEvent(new CustomEvent('iatan:questionAnswered', {
      detail: { questionId, answer },
    }));

    toast.success('Answer submitted');
  } catch (err) {
    toast.error('Failed to submit answer: ' + err.message);
  }
}

// --- Page card ---
function createPageCard(toolName, status, args, result) {
  const path = args?.path || 'page';
  const pageTitle = args?.title || path;
  const actionLabel = getToolLabel(toolName, args);

  const card = h('div', { className: 'chat-card chat-card--page' }, [
    h('div', { className: 'chat-card__header' }, [
      h('span', { innerHTML: icon('file-text'), className: 'chat-card__icon chat-card__icon--page' }),
      h('span', { className: 'chat-card__label' }, actionLabel),
      createStatusBadge(status),
    ]),
    h('div', { className: 'chat-card__body' }, [
      h('strong', {}, pageTitle),
      h('code', { className: 'text-sm text-secondary ml-2' }, path),
    ]),
    h('div', { className: 'chat-card__actions' }, [
      h('button', {
        className: 'btn btn--ghost btn--sm',
        onClick: () => switchPanel('pages'),
      }, 'View Pages \u2192'),
    ]),
  ]);

  makeCollapsible(card);

  return { element: card, updateStatus: bindStatusUpdater(card) };
}

// --- Asset card ---
function createAssetCard(toolName, status, args, result) {
  const filename = args?.filename || 'asset';
  const contentType = args?.content_type || '';
  const actionLabel = getToolLabel(toolName, args);

  const card = h('div', { className: 'chat-card chat-card--asset' }, [
    h('div', { className: 'chat-card__header' }, [
      h('span', { innerHTML: icon('image'), className: 'chat-card__icon chat-card__icon--asset' }),
      h('span', { className: 'chat-card__label' }, actionLabel),
      createStatusBadge(status),
    ]),
    h('div', { className: 'chat-card__body' }, [
      h('strong', {}, filename),
      contentType ? h('span', { className: 'text-sm text-secondary ml-2' }, contentType) : null,
    ].filter(Boolean)),
    h('div', { className: 'chat-card__actions' }, [
      h('button', {
        className: 'btn btn--ghost btn--sm',
        onClick: () => switchPanel('assets'),
      }, 'View Assets \u2192'),
    ]),
  ]);

  makeCollapsible(card);

  return { element: card, updateStatus: bindStatusUpdater(card) };
}

// --- Table card ---
function createTableCard(toolName, status, args, result) {
  const tableName = args?.table_name || args?.table || 'table';

  const card = h('div', { className: 'chat-card chat-card--table' }, [
    h('div', { className: 'chat-card__header' }, [
      h('span', { innerHTML: icon('database'), className: 'chat-card__icon chat-card__icon--table' }),
      h('span', { className: 'chat-card__label' }, getToolLabel(toolName, args)),
      createStatusBadge(status),
    ]),
    h('div', { className: 'chat-card__body' }, [
      h('strong', {}, tableName),
    ]),
    h('div', { className: 'chat-card__actions' }, [
      h('button', {
        className: 'btn btn--ghost btn--sm',
        onClick: () => switchPanel('tables'),
      }, 'View Tables \u2192'),
    ]),
  ]);

  makeCollapsible(card);

  return { element: card, updateStatus: bindStatusUpdater(card) };
}

// --- API Endpoint card ---
function createEndpointCard(toolName, status, args, result) {
  const path = args?.path || 'endpoint';
  const tableName = args?.table_name || '';
  const card = h('div', { className: 'chat-card chat-card--table' }, [
    h('div', { className: 'chat-card__header' }, [
      h('span', { innerHTML: icon('zap'), className: 'chat-card__icon chat-card__icon--table' }),
      h('span', { className: 'chat-card__label' }, getToolLabel(toolName, args)),
      createStatusBadge(status),
    ]),
    h('div', { className: 'chat-card__body' }, [
      h('strong', {}, `/api/${path}`),
      tableName ? h('span', { className: 'text-sm text-secondary ml-2' }, `\u2192 ${tableName}`) : null,
    ].filter(Boolean)),
  ]);

  makeCollapsible(card);

  return { element: card, updateStatus: bindStatusUpdater(card) };
}

// Helper to bind a status updater to a card's header badge.
function bindStatusUpdater(card) {
  return (newStatus) => {
    const badge = card.querySelector('.chat-card__header .badge:last-child');
    if (badge) {
      badge.className = newStatus === 'success' ? 'badge badge--success' : 'badge badge--danger';
      badge.textContent = newStatus === 'success' ? 'Done' : 'Error';
    }
  };
}

// Helper to create a running/success/error badge
function createStatusBadge(status) {
  if (status === 'success') return h('span', { className: 'badge badge--success' }, 'Done');
  if (status === 'error') return h('span', { className: 'badge badge--danger' }, 'Error');
  return h('span', { className: 'badge badge--info' }, 'Running');
}

/**
 * Wrap a card with collapsible header + detail section.
 * Auto-expands when status is 'running', collapsed otherwise.
 */
function makeCollapsible(card) {
  const header = card.querySelector('.chat-card__header');
  if (!header) return;

  // Add chevron to the front of the header
  const chevron = h('span', { innerHTML: icon('chevron-right'), className: 'chat-card__chevron' });
  header.insertBefore(chevron, header.firstChild);

  // Wrap body + actions in a detail container
  const detail = h('div', { className: 'chat-card__detail' });
  const children = Array.from(card.children).filter(c => c !== header);
  for (const child of children) {
    detail.appendChild(child);
  }
  card.appendChild(detail);

  // Auto-expand if running
  const badge = header.querySelector('.badge');
  const isRunning = badge && badge.classList.contains('badge--info');
  if (isRunning) {
    header.classList.add('expanded');
    detail.classList.add('visible');
  }

  // Toggle on header click
  header.addEventListener('click', () => {
    header.classList.toggle('expanded');
    detail.classList.toggle('visible');
  });
}

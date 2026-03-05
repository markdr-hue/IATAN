/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

/**
 * Chat message component.
 */

import { h } from '../../core/dom.js';
import { render as renderMarkdown } from '../../core/markdown.js';
import { icon } from '../icon.js';
import { createToolCard, getToolLabel } from './cards.js';

/**
 * Format a compact timestamp (e.g., "2:34 PM").
 * @param {string} [createdAt] — ISO timestamp from the server; defaults to now.
 */
function timestamp(createdAt) {
  const d = createdAt ? new Date(createdAt) : new Date();
  return h('span', { className: 'message__time' }, d.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' }));
}

/**
 * Create a user message element.
 * @param {string} text
 * @param {string} [createdAt] — ISO timestamp from DB history.
 */
export function createUserMessage(text, createdAt) {
  const badge = h('div', { className: 'message__user-badge' }, [
    h('span', { innerHTML: icon('user') }),
    h('span', {}, 'You'),
    timestamp(createdAt),
  ]);

  const msg = h('div', { className: 'message message--user' }, [
    badge,
    h('div', { className: 'message__content' }, text),
  ]);
  return msg;
}

/**
 * Create an assistant message element. Content is rendered as markdown.
 * @param {string} text
 * @param {string} [createdAt] — ISO timestamp from DB history.
 */
export function createAssistantMessage(text, createdAt) {
  const content = h('div', { className: 'message__content' });
  content.innerHTML = renderMarkdown(text);
  const msg = h('div', { className: 'message message--assistant' }, [
    content,
    timestamp(createdAt),
  ]);
  return msg;
}

/**
 * Create a streaming assistant message that can be updated.
 * @returns {{ element: HTMLElement, appendText: Function, finish: Function }}
 */
export function createStreamingMessage() {
  const msg = h('div', { className: 'message message--assistant' });
  const cursor = h('span', { className: 'typing-cursor' });
  msg.appendChild(cursor);

  let buffer = '';

  function appendText(text) {
    buffer += text;
    msg.innerHTML = renderMarkdown(buffer);
    msg.appendChild(cursor);
  }

  function finish() {
    if (cursor.parentNode) {
      cursor.parentNode.removeChild(cursor);
    }
    const content = h('div', { className: 'message__content' });
    content.innerHTML = renderMarkdown(buffer);
    msg.innerHTML = '';
    msg.appendChild(content);
    msg.appendChild(timestamp());
  }

  function getText() {
    return buffer;
  }

  return { element: msg, appendText, finish, getText };
}

/**
 * Create an automated system message element. Used for brain-generated "user"
 * role messages (stage instructions) that the human didn't actually type.
 * @param {string} text
 * @param {string} [createdAt] — ISO timestamp from DB history.
 */
export function createAutomatedMessage(text, createdAt) {
  const badge = h('div', { className: 'message__system-badge' }, [
    h('span', { innerHTML: icon('zap') }),
    h('span', {}, 'You'),
    h('span', { className: 'message__auto-label' }, '(automated)'),
    timestamp(createdAt),
  ]);

  const msg = h('div', { className: 'message message--automated' }, [
    badge,
    h('div', { className: 'message__content' }, text),
  ]);
  return msg;
}

/**
 * Create a brain message element. Like assistant but with brain badge and accent border.
 * @param {string} text
 * @param {string} [createdAt] — ISO timestamp from DB history.
 */
export function createBrainMessage(text, createdAt) {
  const badge = h('div', { className: 'message__brain-badge' }, [
    h('span', { innerHTML: icon('brain') }),
    h('span', {}, 'Brain'),
    timestamp(createdAt),
  ]);

  const body = h('div', { className: 'message__body' });
  body.innerHTML = renderMarkdown(text);

  const msg = h('div', { className: 'message message--brain' }, [badge, body]);
  return msg;
}

/**
 * Extract a 1-line summary from a tool result JSON string.
 */
function extractToolSummary(result) {
  if (!result) return '';
  try {
    const parsed = typeof result === 'string' ? JSON.parse(result) : result;
    if (!parsed) return '';

    // Page operations
    if (parsed.path) return parsed.path;

    // Title (e.g. from tool results)
    if (parsed.title) return parsed.title;

    // File operations
    if (parsed.filename) return parsed.filename;

    // Table operations
    if (parsed.table_name) return parsed.table_name;

    // Data queries
    if (parsed.rows !== undefined) return `${parsed.rows} rows`;
    if (Array.isArray(parsed.data)) return `${parsed.data.length} results`;

    // Count
    if (parsed.count !== undefined) return `${parsed.count} items`;

    // Generic success message
    if (parsed.message) {
      const msg = parsed.message;
      return msg.length > 50 ? msg.slice(0, 47) + '...' : msg;
    }

    return '';
  } catch {
    return '';
  }
}

/**
 * Create a tool call card element.
 * Routes to interactive cards for known tool types, falls back to generic card.
 */
export function createToolCall(toolName, status, result, args) {
  // Try interactive card first
  const card = createToolCard(toolName, status, args, result);
  if (card) return card;

  // Generic fallback
  return createGenericToolCall(toolName, status, result, args);
}

/**
 * Generic tool call card (collapsible, status badge, 1-line summary on completion).
 */
function createGenericToolCall(toolName, status, result, args) {
  const statusBadge = status === 'success'
    ? h('span', { className: 'badge badge--success' }, 'Done')
    : status === 'running'
    ? h('span', { className: 'badge badge--info' }, [
        h('span', { className: 'spinner spinner--sm' }),
        ' Running',
      ])
    : h('span', { className: 'badge badge--danger' }, 'Error');

  const header = h('div', { className: 'message__tool-header' }, [
    h('span', { innerHTML: icon('chevron-right') }),
    h('span', {}, getToolLabel(toolName, args)),
    timestamp(),
    h('span', { className: 'message__tool-status' }, [statusBadge]),
  ]);

  // Add summary if result available at creation
  if (status === 'success' && result) {
    const summary = extractToolSummary(result);
    if (summary) {
      header.insertBefore(
        h('span', { className: 'message__tool-summary' }, summary),
        header.querySelector('.message__tool-status')
      );
    }
  }

  const body = h('div', { className: 'message__tool-body' });
  if (result) {
    body.textContent = typeof result === 'string' ? result : JSON.stringify(result, null, 2);
  }

  const cardEl = h('div', { className: 'message__tool-call' }, [header, body]);

  // Auto-expand while running so the user can see activity
  if (status === 'running') {
    header.classList.add('expanded');
    body.classList.add('visible');
  }

  header.addEventListener('click', () => {
    header.classList.toggle('expanded');
    body.classList.toggle('visible');
  });

  return {
    element: cardEl,
    updateStatus(newStatus, newResult) {
      const newBadge = newStatus === 'success'
        ? h('span', { className: 'badge badge--success' }, 'Done')
        : h('span', { className: 'badge badge--danger' }, 'Error');
      const statusEl = header.querySelector('.message__tool-status');
      statusEl.innerHTML = '';
      statusEl.appendChild(newBadge);
      if (newResult) {
        body.textContent = typeof newResult === 'string' ? newResult : JSON.stringify(newResult, null, 2);

        // Add 1-line summary on completion
        if (newStatus === 'success') {
          const summary = extractToolSummary(newResult);
          if (summary) {
            let summaryEl = header.querySelector('.message__tool-summary');
            if (!summaryEl) {
              summaryEl = h('span', { className: 'message__tool-summary' });
              header.insertBefore(summaryEl, statusEl);
            }
            summaryEl.textContent = summary;
          }
        }
      }
      // Auto-collapse when done
      if (newStatus === 'success') {
        header.classList.remove('expanded');
        body.classList.remove('visible');
      }
    },
  };
}

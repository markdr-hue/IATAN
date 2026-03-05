/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

/**
 * Site chat view — always-visible in the left panel of the two-panel layout.
 * Shows both user-initiated chat and brain activity in a merged timeline.
 * Includes inline question cards, pinned question banner, brain status bar,
 * and answer acknowledgment flow.
 */

import { h, render, clear } from '../../core/dom.js';
import { get } from '../../core/http.js';
import * as state from '../../core/state.js';
import { createFeed } from '../../ui/chat/feed.js';
import { createUserMessage, createAutomatedMessage, createAssistantMessage, createBrainMessage, createStreamingMessage, createToolCall } from '../../ui/chat/message.js';
import { createQuestionCard } from '../../ui/chat/cards.js';
import { startStream, humanizeError } from '../../ui/chat/stream.js';
import { createChatInput } from '../../ui/chat/input.js';
import { icon } from '../../ui/icon.js';
import * as toast from '../../ui/toast.js';

export function renderSiteChat(container, siteId) {
  const feed = createFeed();
  let currentStream = null;
  let streamingMsg = null;
  let emptyState = null;
  const unwatchers = [];
  const questionCards = {}; // Track question cards by ID
  let questionData = {};    // Track question metadata for banner

  // --- Brain status bar (persistent, top of chat) ---
  const brainStatusBar = h('div', { className: 'chat-brain-status' });
  let brainStatusTimer = null;

  function updateBrainStatus(newState, detail) {
    if (brainStatusTimer) clearTimeout(brainStatusTimer);
    brainStatusBar.innerHTML = '';

    if (newState === 'idle') {
      brainStatusBar.style.display = 'none';
      return;
    }

    brainStatusBar.style.display = '';
    const states = {
      thinking: { ico: 'brain', text: 'Brain is thinking...', cls: 'thinking' },
      building: { ico: 'code', text: detail || 'Brain is working...', cls: 'building' },
      waiting:  { ico: 'clock', text: 'Waiting for your answer...', cls: 'waiting' },
    };
    const s = states[newState] || states.thinking;
    brainStatusBar.className = `chat-brain-status chat-brain-status--${s.cls}`;
    brainStatusBar.appendChild(h('span', { innerHTML: icon(s.ico), className: 'chat-brain-status__icon' }));
    brainStatusBar.appendChild(h('span', {}, s.text));

    // Auto-hide after 2 minutes if no update
    brainStatusTimer = setTimeout(() => updateBrainStatus('idle'), 120000);
  }

  // --- Pinned question banner (between feed and input) ---
  const questionBanner = h('div', { className: 'chat-question-banner' });
  questionBanner.style.display = 'none';

  function updateQuestionBanner() {
    const pendingIds = Object.keys(questionCards);
    if (pendingIds.length === 0) {
      questionBanner.style.display = 'none';
      updateBrainStatus('idle');
      return;
    }

    questionBanner.style.display = '';
    questionBanner.innerHTML = '';

    // Get the most recent pending question
    const latestId = pendingIds[pendingIds.length - 1];
    const qMeta = questionData[latestId];
    const questionText = qMeta?.question || 'The brain needs your input';

    // Parse options
    let parsedOptions = [];
    if (qMeta?.options) {
      try {
        parsedOptions = typeof qMeta.options === 'string' ? JSON.parse(qMeta.options) : qMeta.options;
      } catch { /* ignore */ }
    }
    if (!Array.isArray(parsedOptions)) parsedOptions = [];

    const badge = pendingIds.length > 1
      ? h('span', { className: 'badge badge--warning' }, `${pendingIds.length} questions`)
      : h('span', { className: 'badge badge--warning' }, 'Question');

    const textEl = h('div', { className: 'chat-question-banner__text' }, questionText);

    // Quick-reply buttons in banner
    const actions = h('div', { className: 'chat-question-banner__actions' });
    for (const opt of parsedOptions.slice(0, 3)) {
      const label = typeof opt === 'string' ? opt : opt.label || opt;
      actions.appendChild(h('button', {
        className: 'btn btn--sm btn--ghost chat-question-banner__btn',
        onClick: () => {
          // Find the card and submit through it
          const card = questionCards[latestId];
          if (card?.element) {
            card.element.scrollIntoView({ behavior: 'smooth', block: 'center' });
          }
        },
      }, label));
    }

    // Scroll-to-question button
    const scrollBtn = h('button', {
      className: 'btn btn--sm btn--primary',
      onClick: () => {
        const card = questionCards[latestId];
        if (card?.element) {
          card.element.scrollIntoView({ behavior: 'smooth', block: 'center' });
        }
      },
    }, 'Answer');

    questionBanner.appendChild(h('span', { innerHTML: icon('help-circle'), className: 'chat-question-banner__icon' }));
    questionBanner.appendChild(badge);
    questionBanner.appendChild(textEl);
    if (parsedOptions.length > 0) questionBanner.appendChild(actions);
    questionBanner.appendChild(scrollBtn);

    updateBrainStatus('waiting');
  }

  // --- Chat input ---
  const chatInput = createChatInput((text) => {
    sendMessage(text);
  });

  // --- Layout ---
  const chatContainer = h('div', { className: 'chat-container' }, [
    brainStatusBar,
    feed.element,
    questionBanner,
    chatInput.element,
  ]);

  render(container, chatContainer);

  // Load history, then load pending questions
  loadHistory().then(() => loadPendingQuestions());
  chatInput.focus();

  // Subscribe to live brain events for this site
  subscribeBrainEvents();

  // Listen for question answers from cards.js
  function onQuestionAnswered(e) {
    const { questionId, answer } = e.detail;
    // Show a "Your answer" bubble in the feed
    removeEmptyState();
    const answerMsg = h('div', { className: 'message message--user-answer' }, [
      h('span', { className: 'message__answer-label' }, 'Your answer'),
      h('span', {}, answer),
    ]);
    feed.append(answerMsg);
    feed.scrollToBottom();

    // Clean up tracking
    delete questionCards[questionId];
    delete questionData[questionId];
    updateQuestionBanner();
  }
  document.addEventListener('iatan:questionAnswered', onQuestionAnswered);

  // --- Empty state ---
  function showEmptyState() {
    emptyState = h('div', { className: 'chat-empty' }, [
      h('div', { className: 'chat-empty__icon', innerHTML: icon('brain') }),
      h('h3', { className: 'chat-empty__title' }, 'Your AI brain is waking up'),
      h('p', { className: 'chat-empty__text' },
        'The brain will autonomously build your website based on your description. Messages, questions, and build activity will appear here.'
      ),
      h('div', { className: 'chat-empty__hints' }, [
        h('div', { className: 'chat-empty__hint' }, [
          h('span', { innerHTML: icon('message-circle'), className: 'chat-empty__hint-icon' }),
          h('span', {}, 'Ask questions about your site anytime'),
        ]),
        h('div', { className: 'chat-empty__hint' }, [
          h('span', { innerHTML: icon('help-circle'), className: 'chat-empty__hint-icon' }),
          h('span', {}, 'The brain will ask when it needs your input'),
        ]),
        h('div', { className: 'chat-empty__hint' }, [
          h('span', { innerHTML: icon('activity'), className: 'chat-empty__hint-icon' }),
          h('span', {}, 'Build progress shows in real-time'),
        ]),
      ]),
    ]);
    feed.append(emptyState);
  }

  function removeEmptyState() {
    if (emptyState) {
      emptyState.remove();
      emptyState = null;
    }
  }

  let oldestTimestamp = null;
  let hasOlderMessages = true;

  function renderMessages(messages, prepend = false) {
    // First pass: build a map of tool_call_id → result content from role="tool" messages
    const toolResults = {};
    for (const msg of messages) {
      if (msg.role === 'tool' && msg.tool_call_id) {
        toolResults[msg.tool_call_id] = msg.content;
      }
    }

    const elements = [];
    // Second pass: render messages, looking up tool results from the map
    for (const msg of messages) {
      if (msg.role === 'user') {
        if (msg.session_id === 'brain') {
          elements.push(createAutomatedMessage(msg.content, msg.created_at));
        } else {
          elements.push(createUserMessage(msg.content, msg.created_at));
        }
      } else if (msg.role === 'assistant') {
        const isBrain = msg.session_id === 'brain';
        if (isBrain) {
          if (msg.content) elements.push(createBrainMessage(msg.content, msg.created_at));
        } else {
          if (msg.content) elements.push(createAssistantMessage(msg.content, msg.created_at));
        }
        if (msg.tool_calls && msg.tool_calls.length > 0) {
          for (const tc of msg.tool_calls) {
            const name = tc.name || tc.function?.name || 'tool';
            let args = {};
            try {
              if (tc.arguments) args = typeof tc.arguments === 'string' ? JSON.parse(tc.arguments) : tc.arguments;
            } catch { /* ignore */ }
            // Skip question tools in history — shown as interactive question cards instead
            if (name === 'ask_question' || (name === 'manage_communication' && args.action === 'ask')) continue;
            const result = toolResults[tc.id];
            const toolEl = createToolCall(name, 'success', result, args);
            elements.push(toolEl.element);
          }
        }
      }
      // Skip role="tool" — results are shown inline with their parent assistant's tool calls
    }

    if (prepend) {
      // Remove existing "load earlier" button if present
      const existing = feed.element.querySelector('.load-earlier-btn');
      if (existing) existing.remove();
      // Prepend elements at the top
      const firstChild = feed.element.firstChild;
      for (const el of elements) {
        feed.element.insertBefore(el, firstChild);
      }
    } else {
      for (const el of elements) {
        feed.append(el);
      }
    }
  }

  async function loadHistory() {
    try {
      const messages = await get(`/admin/api/chat/${siteId}/history`);

      if (!messages || !Array.isArray(messages) || messages.length === 0) {
        showEmptyState();
        hasOlderMessages = false;
        return;
      }

      if (messages.length > 0) {
        oldestTimestamp = messages[0].created_at;
        hasOlderMessages = messages.length >= 50;
      }

      renderMessages(messages);

      if (hasOlderMessages) {
        addLoadEarlierButton();
      }

      feed.scrollToBottom(true);
    } catch (err) {
      // Silently handle history load failures
    }
  }

  function addLoadEarlierButton() {
    const btn = h('div', {
      className: 'load-earlier-btn',
      style: 'text-align:center;padding:0.5rem',
    }, [
      h('button', {
        className: 'btn btn--ghost btn--sm',
        onClick: loadEarlierMessages,
      }, 'Load earlier messages'),
    ]);
    feed.element.insertBefore(btn, feed.element.firstChild);
  }

  async function loadEarlierMessages() {
    if (!oldestTimestamp || !hasOlderMessages) return;
    try {
      const messages = await get(`/admin/api/chat/${siteId}/history?before=${encodeURIComponent(oldestTimestamp)}`);
      if (!messages || messages.length === 0) {
        hasOlderMessages = false;
        const existing = feed.element.querySelector('.load-earlier-btn');
        if (existing) existing.remove();
        return;
      }
      oldestTimestamp = messages[0].created_at;
      hasOlderMessages = messages.length >= 50;

      renderMessages(messages, true);

      if (hasOlderMessages) {
        addLoadEarlierButton();
      }
    } catch (err) {
      // Silently handle load failures
    }
  }

  async function loadPendingQuestions() {
    try {
      const questions = await get(`/admin/api/sites/${siteId}/questions?status=pending`);
      if (questions && questions.length > 0) {
        removeEmptyState();
        for (const q of questions) {
          if (!questionCards[q.id]) {
            const qCard = createQuestionCard(q);
            questionCards[q.id] = qCard;
            questionData[q.id] = q;
            feed.append(qCard.element);
          }
        }
        feed.scrollToBottom();
        updateQuestionBanner();
      }
    } catch {
      // Questions endpoint may not have data yet
    }
  }

  function subscribeBrainEvents() {
    const brainToolCards = {};

    unwatchers.push(state.watch('brainMessage', (data) => {
      if (!data || String(data.site_id) !== String(siteId)) return;
      if (data.content) {
        removeEmptyState();
        feed.append(createBrainMessage(data.content));
        feed.scrollToBottom();
        updateBrainStatus('idle');
      }
    }));

    unwatchers.push(state.watch('brainToolStart', (data) => {
      if (!data || String(data.site_id) !== String(siteId)) return;
      const toolName = data.tool || data.name || 'tool';
      // Skip question tools — the interactive question card is rendered separately.
      const isQuestion = toolName === 'ask_question' || (toolName === 'manage_communication' && data.args?.action === 'ask');
      if (isQuestion) return;
      removeEmptyState();
      const tc = createToolCall(toolName, 'running', null, data.args || {});
      const key = data.call_id || data.tool || data.name;
      brainToolCards[key] = tc;
      feed.append(tc.element);
      feed.scrollToBottom();
      updateBrainStatus('building', `Working: ${tc.element.querySelector('.chat-card__label, .message__tool-header span:nth-child(2)')?.textContent || toolName}`);
    }));

    unwatchers.push(state.watch('brainToolResult', (data) => {
      if (!data || String(data.site_id) !== String(siteId)) return;
      const toolName = data.tool || data.name || 'tool';
      const isQuestion = toolName === 'ask_question' || (toolName === 'manage_communication' && data.args?.action === 'ask');
      if (isQuestion) return;
      const key = data.call_id || data.tool || data.name;
      if (brainToolCards[key]) {
        brainToolCards[key].updateStatus(
          data.error ? 'error' : 'success',
          data.result || data.error
        );
      }
      feed.scrollToBottom();
    }));

    unwatchers.push(state.watch('brainTick', (data) => {
      if (!data || String(data.site_id) !== String(siteId)) return;
      if (data.status === 'starting') {
        removeEmptyState();
        updateBrainStatus('thinking');
      }
    }));

    // Watch for new questions asked by brain
    unwatchers.push(state.watch('questionAsked', (data) => {
      if (!data || String(data.site_id) !== String(siteId)) return;
      if (data.question && !questionCards[data.id]) {
        removeEmptyState();
        const qCard = createQuestionCard(data);
        questionCards[data.id] = qCard;
        questionData[data.id] = data;
        feed.append(qCard.element);
        feed.scrollToBottom();
        updateQuestionBanner();
      }
    }));

    // Watch for questions answered elsewhere
    unwatchers.push(state.watch('questionAnswered', (data) => {
      if (!data) return;
      const qId = data.question_id;
      if (questionCards[qId]) {
        questionCards[qId].element.remove();
        delete questionCards[qId];
        delete questionData[qId];
        updateQuestionBanner();
      }
    }));
  }

  function sendMessage(text) {
    removeEmptyState();
    feed.append(createUserMessage(text));
    chatInput.disable();

    streamingMsg = createStreamingMessage();
    feed.append(streamingMsg.element);

    const toolCards = {};

    currentStream = startStream(
      `/admin/api/chat/${siteId}/stream`,
      JSON.stringify({ message: text, session_id: 'admin' }),
      {
        onToken(token) {
          if (streamingMsg) {
            streamingMsg.appendText(token);
            feed.scrollToBottom();
          }
        },
        onToolStart(data) {
          const tc = createToolCall(data.name || data.tool, 'running', null, data.args || {});
          toolCards[data.id || data.name] = tc;
          feed.append(tc.element);
          feed.scrollToBottom();
        },
        onToolResult(data) {
          const id = data.id || data.name;
          if (toolCards[id]) {
            toolCards[id].updateStatus(
              data.error ? 'error' : 'success',
              data.result || data.error
            );
          }
          feed.scrollToBottom();
        },
        onDone() {
          if (streamingMsg) {
            streamingMsg.finish();
            streamingMsg = null;
          }
          chatInput.enable();
          chatInput.focus();
          currentStream = null;
        },
        onError(err) {
          if (streamingMsg) {
            streamingMsg.appendText(`\n\n*Error: ${err}*`);
            streamingMsg.finish();
            streamingMsg = null;
          }
          toast.error('Chat error: ' + err);
          chatInput.enable();
          chatInput.focus();
          currentStream = null;
        },
      }
    );
  }

  return function cleanup() {
    if (currentStream) currentStream.abort();
    if (brainStatusTimer) clearTimeout(brainStatusTimer);
    document.removeEventListener('iatan:questionAnswered', onQuestionAnswered);
    for (const unwatch of unwatchers) {
      unwatch();
    }
  };
}

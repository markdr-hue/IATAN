/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

/**
 * Site home view — stats bar (top) + chat (bottom), full width.
 */

import { h, render, clear } from '../../core/dom.js';
import { get } from '../../core/http.js';
import { icon } from '../../ui/icon.js';
import * as state from '../../core/state.js';
import * as toast from '../../ui/toast.js';
import { renderSiteChat } from './chat.js';

export function renderSiteHome(container, siteId) {
  const unwatchers = [];
  let chatCleanup = null;

  // Stats bar (top ~25%)
  const statsBar = h('div', { className: 'home-stats-bar' });

  // Chat panel (bottom ~75%)
  const chatPanel = h('div', { className: 'home-chat-panel' });

  const homeLayout = h('div', { className: 'home-layout' }, [
    statsBar,
    chatPanel,
  ]);

  render(container, homeLayout);

  // Render chat
  chatCleanup = renderSiteChat(chatPanel, siteId);

  // Refs for targeted DOM updates
  let pagesValueEl = null;
  let brainStatusEl = null;
  let brainStatusDot = null;
  let brainModeEl = null;

  // Load initial stats
  loadStats(statsBar, siteId);

  // --- Real-time SSE watchers ---

  // Pages refresh when modified
  unwatchers.push(state.watch('toolExecuted', (evt) => {
    if (!evt || evt.site_id !== siteId || !evt.success) return;
    if (['save_page', 'delete_page'].includes(evt.tool)) {
      refreshPages(siteId);
    }
  }));

  // Brain status updates
  unwatchers.push(state.watch('runningSites', () => {
    const running = (state.get('runningSites') || []).includes(siteId);
    if (brainStatusEl) brainStatusEl.textContent = running ? 'Running' : 'Stopped';
    if (brainStatusDot) brainStatusDot.className = `home-stat-dot${running ? ' home-stat-dot--active' : ''}`;
  }));

  // Brain mode changes (building → monitoring → complete)
  unwatchers.push(state.watch('brainModeChanged', (data) => {
    if (!data || data.site_id !== siteId) return;
    if (brainModeEl) brainModeEl.textContent = `Mode: ${data.mode}`;
  }));

  async function loadStats(bar, sid) {
    clear(bar);

    try {
      const [summary, brainStatus] = await Promise.all([
        get(`/admin/api/sites/${sid}/summary`),
        get(`/admin/api/brain/${sid}/status`),
      ]);

      clear(bar);
      const grid = h('div', { className: 'home-stats-grid' });

      // 1. Brain Status — use `running` flag (worker exists) not just state
      const isRunning = brainStatus.running || !['idle', 'unknown'].includes(brainStatus.state);
      brainStatusDot = h('span', { className: `home-stat-dot${isRunning ? ' home-stat-dot--active' : ''}` });
      const brainCard = createStatCard(
        'Brain',
        isRunning ? 'Running' : 'Stopped',
        `Mode: ${brainStatus.mode || 'building'}`,
        'brain',
        [brainStatusDot],
      );
      brainStatusEl = brainCard.valueEl;
      brainModeEl = brainCard.subEl;
      grid.appendChild(brainCard.el);

      // 2. Tokens
      const tokensCard = createStatCard(
        'Tokens Used',
        summary.total_tokens.toLocaleString(),
        `${summary.brain_actions} brain actions`,
        'zap',
      );
      grid.appendChild(tokensCard.el);

      // 3. Pages
      const pagesCard = createStatCard('Pages', String(summary.pages_count), 'published', 'file-text');
      pagesValueEl = pagesCard.valueEl;
      grid.appendChild(pagesCard.el);

      // 4. Visitors (24h)
      const viewsCard = createStatCard(
        'Visitors (24h)',
        String(summary.unique_visitors),
        `${summary.page_views} page views`,
        'activity',
      );
      grid.appendChild(viewsCard.el);

      bar.appendChild(grid);

      // Re-apply runningSites state to catch SSE updates that arrived during loading.
      const latestRunning = (state.get('runningSites') || []).includes(sid);
      if (latestRunning !== isRunning) {
        if (brainStatusEl) brainStatusEl.textContent = latestRunning ? 'Running' : 'Stopped';
        if (brainStatusDot) brainStatusDot.className = `home-stat-dot${latestRunning ? ' home-stat-dot--active' : ''}`;
      }
    } catch (err) {
      clear(bar);
      bar.appendChild(h('div', { className: 'home-stats-error' }, 'Failed to load stats'));
      toast.error('Stats: ' + err.message);
    }
  }

  function createStatCard(label, value, sub, iconName, extraChildren) {
    const valueEl = h('div', { className: 'stat-card__value' }, value);
    const subEl = h('div', { className: 'stat-card__sub' }, sub);

    const headerRow = h('div', { className: 'flex items-center justify-between' }, [
      h('span', { className: 'stat-card__label' }, label),
      h('span', { innerHTML: icon(iconName), style: { color: 'var(--text-tertiary)' } }),
    ]);

    const children = [headerRow, valueEl, subEl];
    if (extraChildren) children.push(...extraChildren);

    const el = h('div', { className: 'stat-card stat-card--compact' }, children);
    return { el, valueEl, subEl };
  }

  async function refreshPages(sid) {
    try {
      const pages = await get(`/admin/api/sites/${sid}/pages`);
      if (pagesValueEl) pagesValueEl.textContent = String(pages.length);
    } catch { /* ignore */ }
  }

  return function cleanup() {
    if (chatCleanup) chatCleanup();
    for (const unwatch of unwatchers) unwatch();
  };
}

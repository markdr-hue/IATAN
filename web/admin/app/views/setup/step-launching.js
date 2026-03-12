/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

/**
 * Setup Step 5: Launch animation with animated checklist.
 */

import { h } from '../../core/dom.js';
import { icon } from '../../ui/icon.js';
import { post } from '../../core/http.js';
import * as toast from '../../ui/toast.js';

const CHECKLIST = [
  { label: 'Creating your account' },
  { label: 'Connecting AI provider' },
  { label: 'Setting up your site' },
  { label: 'Activating the brain' },
  { label: 'Ready to go' },
];

export function renderLaunching(container, setupData) {
  const items = CHECKLIST.map((item) => {
    const itemIcon = h('div', { className: 'launch-item__icon' });
    const el = h('div', { className: 'launch-item' }, [
      itemIcon,
      h('span', {}, item.label),
    ]);
    return { el, itemIcon };
  });

  const title = setupData.displayName
    ? `Hang tight ${setupData.displayName}`
    : 'Setting things up';

  const content = h('div', {}, [
    h('div', { className: 'setup-card__header' }, [
      h('div', { className: 'setup-card__icon', innerHTML: icon('zap') }),
      h('h2', { className: 'setup-card__title' }, title),
      h('p', { className: 'setup-card__desc' }, 'This only takes a few seconds.'),
    ]),
    h('div', { className: 'launch-checklist' },
      items.map(i => i.el)
    ),
  ]);

  container.appendChild(content);

  // Animated step progression
  async function runSetup() {
    function markActive(index) {
      if (index < items.length) {
        items[index].el.classList.add('active');
        items[index].itemIcon.innerHTML = `<span class="spinner spinner--sm"></span>`;
      }
    }

    function markDone(index) {
      if (index < items.length) {
        items[index].el.classList.remove('active');
        items[index].el.classList.add('done');
        items[index].itemIcon.innerHTML = icon('check');
      }
    }

    function markError(index) {
      if (index < items.length) {
        items[index].el.style.color = 'var(--danger)';
        items[index].itemIcon.innerHTML = icon('x');
      }
    }

    function markSkipped(index) {
      if (index < items.length) {
        items[index].el.classList.remove('active');
        items[index].el.style.color = 'var(--text-tertiary)';
        items[index].itemIcon.innerHTML = `<span style="color: var(--text-tertiary)">\u2014</span>`;
      }
    }

    let errorStep = -1;

    try {
      // Step 1: Create admin account + optional provider
      errorStep = 0;
      markActive(0);
      await delay(500);

      const setupPayload = {
        username: setupData.username,
        password: setupData.password,
        display_name: setupData.displayName || '',
      };

      if (setupData.providerId) {
        setupPayload.provider_id = setupData.providerId;
        if (setupData.modelId) setupPayload.model_id = setupData.modelId;
        if (setupData.apiKey) setupPayload.api_key = setupData.apiKey;
      }

      const result = await post('/admin/api/setup', setupPayload);
      localStorage.setItem('iatan_token', result.token);
      localStorage.setItem('iatan_user', JSON.stringify(result.user));
      markDone(0);

      // Step 2: Provider
      errorStep = 1;
      markActive(1);
      await delay(600);
      if (setupData.providerId) {
        markDone(1);
      } else {
        markSkipped(1);
      }

      // Step 3: Create site
      errorStep = 2;
      markActive(2);
      await delay(400);

      let createdSiteId = null;
      if (setupData.siteName) {
        const { post: authPost } = await import('../../core/http.js');
        const site = await authPost('/admin/api/sites', {
          name: setupData.siteName,
          domain: setupData.siteDomain || null,
          direction: setupData.siteDirection || null,
          llm_model_id: setupData.llmModelId || null,
        });
        createdSiteId = site.id;
        markDone(2);

        // Step 4: Start brain
        errorStep = 3;
        markActive(3);
        try {
          await authPost(`/admin/api/brain/${site.id}/start`, {});
        } catch (e) {
          // Brain may auto-start via event; don't block setup.
        }
        markDone(3);
      } else {
        markSkipped(2);
        markSkipped(3);
      }

      // Step 5: Ready
      errorStep = 4;
      markActive(4);
      await delay(400);
      markDone(4);

      await delay(800);
      // Redirect to site home if a site was created, otherwise dashboard
      window.location.hash = createdSiteId ? `#/sites/${createdSiteId}/home` : '#/dashboard';
      window.location.reload();
    } catch (err) {
      markError(errorStep >= 0 ? errorStep : 0);
      toast.error('Setup failed: ' + err.message);
    }
  }

  runSetup();
}

function delay(ms) {
  return new Promise(resolve => setTimeout(resolve, ms));
}

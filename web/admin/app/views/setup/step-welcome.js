/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

/**
 * Setup Step 1: Welcome splash with name input.
 */

import { h } from '../../core/dom.js';
import { icon } from '../../ui/icon.js';

export function renderWelcome(container, setupData, onNext) {
  function submit() {
    setupData.displayName = nameInput.value.trim();
    onNext();
  }

  const nameInput = h('input', {
    className: 'input',
    type: 'text',
    placeholder: 'Your first name',
    value: setupData.displayName || '',
    style: { textAlign: 'center', maxWidth: '280px', margin: '0 auto' },
    onkeydown: (e) => {
      if (e.key === 'Enter') {
        e.preventDefault();
        submit();
      }
    },
  });

  const content = h('div', {}, [
    h('div', { className: 'setup-card__header' }, [
      h('div', { className: 'setup-card__icon', innerHTML: icon('zap') }),
      h('h2', { className: 'setup-card__title' }, 'Welcome to IATAN'),
      h('p', { className: 'setup-card__desc' },
        'I build websites autonomously. Tell me what you need \u2014 pages, forms, APIs, databases \u2014 and I\u2019ll handle it.'
      ),
    ]),
    h('div', { className: 'form-group text-center mt-6' }, [
      h('label', { style: { marginBottom: '8px', display: 'block' } }, 'What should I call you?'),
      nameInput,
    ]),
    h('div', { className: 'setup-actions setup-actions--center' }, [
      h('button', {
        className: 'btn btn--primary btn--lg',
        onClick: submit,
      }, 'Continue'),
    ]),
    h('p', { className: 'setup-hint' }, 'Press Enter to continue'),
  ]);

  container.appendChild(content);
  nameInput.focus();
}

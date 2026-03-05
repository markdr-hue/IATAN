/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

/**
 * Questions management view.
 */

import { h, clear } from '../../core/dom.js';
import { get, post } from '../../core/http.js';
import { icon } from '../../ui/icon.js';
import * as toast from '../../ui/toast.js';
import * as modal from '../../ui/modal.js';
import { emptyState } from '../../ui/helpers.js';

export async function renderQuestions(container) {
  clear(container);

  const header = h('div', { className: 'context-panel__header context-panel__header--page' }, [
    h('h3', { className: 'context-panel__title context-panel__title--page' }, 'Questions'),
  ]);

  const listContainer = h('div', { className: 'context-panel__body context-panel__body--page' });

  container.appendChild(header);
  container.appendChild(listContainer);

  async function loadQuestions() {
    try {
      const questions = await get('/admin/api/questions');
      renderList(questions);
    } catch (err) {
      toast.error('Failed to load questions: ' + err.message);
    }
  }

  function renderList(questions) {
    clear(listContainer);

    if (questions.length === 0) {
      listContainer.appendChild(emptyState('No questions yet.'));
      return;
    }

    const cards = questions.map(q => {
      const isApproval = q.type === 'approval';

      const statusBadge = q.status === 'pending'
        ? h('span', { className: `badge ${isApproval ? 'badge--danger' : 'badge--warning'}` },
            isApproval ? 'Approval Needed' : 'Pending')
        : h('span', { className: 'badge badge--success' }, 'Answered');

      const actions = [];
      if (q.status === 'pending') {
        if (isApproval) {
          actions.push(h('button', {
            className: 'btn btn--success btn--sm',
            onClick: () => submitAnswer(q.id, 'Approve'),
          }, 'Approve'));
          actions.push(h('button', {
            className: 'btn btn--danger btn--sm',
            onClick: () => submitAnswer(q.id, 'Deny'),
          }, 'Deny'));
        } else {
          actions.push(h('button', {
            className: 'btn btn--primary btn--sm',
            onClick: () => showAnswerModal(q),
          }, [
            h('span', { innerHTML: icon('chat') }),
            'Answer',
          ]));
        }
      }

      const children = [
        h('div', { className: 'card__header' }, [
          h('div', { className: 'flex items-center gap-3' }, [
            h('span', { innerHTML: icon(isApproval ? 'alert-circle' : 'help-circle') }),
            h('div', {}, [
              h('h4', { className: 'card__title' }, `Site #${q.site_id}`),
              h('span', { className: 'text-xs text-secondary' }, new Date(q.created_at).toLocaleString()),
            ]),
          ]),
          statusBadge,
        ]),
        h('div', { className: 'mt-3' }, [
          h('p', {}, q.question),
        ]),
      ];

      if (q.answer) {
        children.push(h('div', { className: 'mt-3', style: { opacity: '0.7' } }, [
          h('strong', {}, 'Answer: '),
          h('span', {}, q.answer),
        ]));
      }

      if (actions.length > 0) {
        children.push(h('div', { className: 'flex items-center gap-2 mt-4' }, actions));
      }

      return h('div', { className: `card mb-4${isApproval && q.status === 'pending' ? ' card--approval' : ''}` }, children);
    });

    cards.forEach(c => listContainer.appendChild(c));
  }

  async function submitAnswer(questionId, answer) {
    try {
      await post(`/admin/api/questions/${questionId}/answer`, { answer });
      toast.success(`Response submitted: ${answer}`);
      loadQuestions();
    } catch (err) {
      toast.error(err.message);
    }
  }

  function showAnswerModal(question) {
    const answerInput = h('textarea', {
      className: 'input',
      rows: 4,
      placeholder: 'Type your answer...',
    });

    const content = h('div', {}, [
      h('div', { className: 'form-group' }, [
        h('label', {}, 'Question'),
        h('p', { style: { padding: '8px 0' } }, question.question),
      ]),
      h('div', { className: 'form-group' }, [
        h('label', {}, 'Your Answer'),
        answerInput,
      ]),
    ]);

    modal.show('Answer Question', content, [
      { label: 'Cancel', onClick: () => {} },
      {
        label: 'Submit Answer',
        className: 'btn btn--primary',
        onClick: async () => {
          try {
            await post(`/admin/api/questions/${question.id}/answer`, {
              answer: answerInput.value.trim(),
            });
            toast.success('Answer submitted');
            loadQuestions();
          } catch (err) {
            toast.error(err.message);
            return false;
          }
        },
      },
    ]);
  }

  loadQuestions();
}

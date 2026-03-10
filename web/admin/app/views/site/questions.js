/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

/**
 * Site-scoped questions panel.
 */

import { h, clear } from '../../core/dom.js';
import { get, post } from '../../core/http.js';
import * as toast from '../../ui/toast.js';
import * as modal from '../../ui/modal.js';
import { emptyState } from '../../ui/helpers.js';

export async function renderSiteQuestions(container, siteId) {
  clear(container);

  const header = h('div', { className: 'context-panel__header' }, [
    h('h3', { className: 'context-panel__title' }, 'Questions'),
  ]);

  const body = h('div', { className: 'context-panel__body' });
  container.appendChild(header);
  container.appendChild(body);

  try {
    const questions = await get(`/admin/api/sites/${siteId}/questions`);
    renderList(questions);
  } catch (err) {
    toast.error('Failed to load questions: ' + err.message);
  }

  function renderList(questions) {
    clear(body);

    if (questions.length === 0) {
      body.appendChild(emptyState('No questions yet. Questions from the AI will appear here.'));
      return;
    }

    for (const q of questions) {
      const isApproval = q.type === 'approval';

      const statusBadge = q.status === 'pending'
        ? h('span', { className: `badge ${isApproval ? 'badge--danger' : 'badge--warning'}` },
            isApproval ? 'Approval Needed' : 'Pending')
        : h('span', { className: 'badge badge--success' }, 'Answered');

      const children = [
        h('div', { className: 'flex items-center justify-between mb-2' }, [
          statusBadge,
          h('span', { className: 'text-xs text-secondary' },
            new Date(q.created_at).toLocaleString()
          ),
        ]),
        h('p', { style: { marginBottom: '8px' } }, q.question),
      ];

      if (q.type === 'secret') {
        children.push(h('span', { className: 'badge badge--info ml-2', style: { fontSize: 'var(--text-xs)' } }, 'Secret'));
      }

      if (q.answer) {
        children.push(h('div', { style: { opacity: '0.7', fontSize: 'var(--text-sm)' } }, [
          h('strong', {}, 'Answer: '),
          h('span', {}, q.answer),
        ]));
      }

      if (q.status === 'pending') {
        if (isApproval) {
          // Render Approve / Deny buttons directly.
          children.push(h('div', { className: 'flex items-center gap-2 mt-3' }, [
            h('button', {
              className: 'btn btn--success btn--sm',
              onClick: () => submitAnswer(q.id, 'Approve'),
            }, 'Approve'),
            h('button', {
              className: 'btn btn--danger btn--sm',
              onClick: () => submitAnswer(q.id, 'Deny'),
            }, 'Deny'),
          ]));
        } else {
          children.push(h('div', { className: 'mt-3' }, [
            h('button', {
              className: 'btn btn--primary btn--sm',
              onClick: () => showAnswerModal(q),
            }, 'Answer'),
          ]));
        }
      }

      body.appendChild(h('div', { className: `card mb-3${isApproval && q.status === 'pending' ? ' card--approval' : ''}` }, children));
    }
  }

  async function submitAnswer(questionId, answer) {
    try {
      await post(`/admin/api/questions/${questionId}/answer`, { answer });
      toast.success(`Response submitted: ${answer}`);
      const questions = await get(`/admin/api/sites/${siteId}/questions`);
      renderList(questions);
    } catch (err) {
      toast.error(err.message);
    }
  }

  function showAnswerModal(question) {
    const isSecret = question.type === 'secret';

    // Parse structured fields if present.
    let parsedFields = [];
    if (question.fields) {
      try {
        parsedFields = typeof question.fields === 'string' ? JSON.parse(question.fields) : question.fields;
      } catch { /* ignore */ }
    }
    if (!Array.isArray(parsedFields)) parsedFields = [];

    const contentChildren = [
      h('div', { className: 'form-group' }, [
        h('label', {}, 'Question'),
        h('p', { style: { padding: '8px 0' } }, question.question),
      ]),
    ];

    let getAnswer;

    if (parsedFields.length > 0) {
      // Multi-field structured input form.
      const fieldInputs = {};
      for (const field of parsedFields) {
        const inputType = field.type === 'secret' ? 'password' : 'text';
        const input = h('input', {
          type: inputType,
          className: 'input',
          placeholder: field.label || field.name,
        });
        fieldInputs[field.name] = input;
        contentChildren.push(h('div', { className: 'form-group' }, [
          h('label', {}, field.label || field.name),
          field.type === 'secret' ? h('p', {
            className: 'text-sm text-secondary',
            style: { opacity: 0.7, marginBottom: '4px' },
          }, 'This value will be encrypted.') : null,
          input,
        ].filter(Boolean)));
      }
      getAnswer = () => {
        const values = {};
        let hasValue = false;
        for (const field of parsedFields) {
          const val = fieldInputs[field.name].value.trim();
          if (val) hasValue = true;
          values[field.name] = val;
        }
        return hasValue ? JSON.stringify(values) : '';
      };
    } else {
      // Single input (legacy path).
      const answerInput = isSecret
        ? h('input', {
            type: 'password',
            className: 'input',
            placeholder: 'Enter secret value...',
          })
        : h('textarea', {
            className: 'input',
            rows: 4,
            placeholder: 'Type your answer...',
          });

      if (isSecret) {
        contentChildren.push(h('p', {
          className: 'text-sm text-secondary mb-3',
          style: { opacity: 0.7 },
        }, `This value will be encrypted and stored as secret '${question.secret_name}'.`));
      }
      contentChildren.push(h('div', { className: 'form-group' }, [
        h('label', {}, isSecret ? 'Secret Value' : 'Your Answer'),
        answerInput,
      ]));
      getAnswer = () => answerInput.value.trim();
    }

    const content = h('div', {}, contentChildren);

    modal.show('Answer Question', content, [
      { label: 'Cancel', onClick: () => {} },
      {
        label: 'Submit',
        className: 'btn btn--primary',
        onClick: async () => {
          const answer = getAnswer();
          if (!answer) return false;
          try {
            await post(`/admin/api/questions/${question.id}/answer`, { answer });
            toast.success('Answer submitted');
            const questions = await get(`/admin/api/sites/${siteId}/questions`);
            renderList(questions);
          } catch (err) {
            toast.error(err.message);
            return false;
          }
        },
      },
    ]);
  }
}

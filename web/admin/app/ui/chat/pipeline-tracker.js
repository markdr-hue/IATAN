/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

/**
 * Pipeline stage tracker widget.
 * Displays a visual progress bar for the brain pipeline stages:
 * PLAN → BUILD → COMPLETE → MONITORING
 */

import { h } from '../../core/dom.js';
import { icon } from '../icon.js';

const STAGES = [
  { key: 'PLAN', label: 'Plan', icon: 'search' },
  { key: 'BUILD', label: 'Build', icon: 'zap' },
  { key: 'COMPLETE', label: 'Complete', icon: 'check' },
  { key: 'MONITORING', label: 'Monitor', icon: 'activity' },
];

const STAGE_ALIASES = { UPDATE_PLAN: 'PLAN' };

export function createPipelineTracker() {
  let visible = false;
  let currentStageKey = null;
  let completedStages = new Set();
  let errorStage = null;

  const trackerEl = h('div', { className: 'pipeline-tracker' });
  trackerEl.style.display = 'none';

  const trackerNodes = {};
  const trackerLines = {};

  const stagesRow = h('div', { className: 'pipeline-tracker__stages' });
  STAGES.forEach((s, i) => {
    if (i > 0) {
      const line = h('div', { className: 'pipeline-tracker__line' });
      stagesRow.appendChild(line);
      trackerLines[s.key] = line;
    }
    const circle = h('div', { className: 'pipeline-tracker__circle' });
    circle.innerHTML = icon(s.icon);
    const label = h('span', { className: 'pipeline-tracker__label' }, s.label);
    const node = h('div', { className: 'pipeline-tracker__node' }, [circle, label]);
    stagesRow.appendChild(node);
    trackerNodes[s.key] = { node, circle, label };
  });
  trackerEl.appendChild(stagesRow);

  function show() {
    if (!visible) {
      visible = true;
      trackerEl.style.display = '';
    }
  }

  function hide() {
    visible = false;
    trackerEl.style.display = 'none';
  }

  function reset() {
    hide();
    currentStageKey = null;
    completedStages = new Set();
    errorStage = null;
    for (const s of STAGES) {
      trackerNodes[s.key].circle.className = 'pipeline-tracker__circle';
      trackerNodes[s.key].label.className = 'pipeline-tracker__label';
      if (trackerLines[s.key]) trackerLines[s.key].className = 'pipeline-tracker__line';
    }
  }

  function setStage(stageKey) {
    stageKey = STAGE_ALIASES[stageKey] || stageKey;
    currentStageKey = stageKey;
    show();

    const idx = STAGES.findIndex(s => s.key === stageKey);
    if (idx < 0) return;

    for (let i = 0; i < idx; i++) {
      completedStages.add(STAGES[i].key);
    }

    for (let i = 0; i < STAGES.length; i++) {
      const s = STAGES[i];
      const { circle, label } = trackerNodes[s.key];
      circle.className = 'pipeline-tracker__circle';
      label.className = 'pipeline-tracker__label';

      if (completedStages.has(s.key)) {
        circle.classList.add('pipeline-tracker__circle--done');
        label.classList.add('pipeline-tracker__label--done');
      } else if (s.key === stageKey && errorStage !== stageKey) {
        circle.classList.add(stageKey === 'MONITORING' ? 'pipeline-tracker__circle--monitoring' : 'pipeline-tracker__circle--active');
        label.classList.add('pipeline-tracker__label--active');
      } else if (s.key === errorStage) {
        circle.classList.add('pipeline-tracker__circle--error');
      }

      if (trackerLines[s.key]) {
        trackerLines[s.key].className = 'pipeline-tracker__line';
        if (completedStages.has(s.key) || (i <= idx && completedStages.has(STAGES[i - 1]?.key))) {
          trackerLines[s.key].classList.add('pipeline-tracker__line--done');
        }
      }
    }
  }

  function markCompleted(stageKey) {
    const norm = STAGE_ALIASES[stageKey] || stageKey;
    completedStages.add(norm);
  }

  function setError(stageKey) {
    errorStage = stageKey;
    if (currentStageKey) setStage(currentStageKey);
  }

  return {
    element: trackerEl,
    setStage,
    show,
    hide,
    reset,
    markCompleted,
    setError,
    // No-op stubs so callers don't break.
    setDetail() {},
    updateBuildStat() {},
    incrementBuildStat() {},
    updateBrainStatus() {},
    cleanup() {},
    get currentStageKey() { return currentStageKey; },
  };
}

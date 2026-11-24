// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// Review suggestions: commit one, or collect several in a batch and commit them as one commit.
// The batch lives in this page only; nothing is stored on the server until it is committed.
import {POST} from '../modules/fetch.js';
import {showErrorToast} from '../modules/toast.js';

const batch = new Set(); // comment IDs
let pending = []; // comment IDs of the open commit form

function blocks() {
  return document.querySelectorAll('.damast-suggestion[data-apply-url]');
}

function toggle(el, show) {
  el?.classList.toggle('tw-hidden', !show);
}

function render() {
  for (const block of blocks()) {
    const inBatch = batch.has(block.getAttribute('data-comment-id'));
    toggle(block.querySelector('.damast-suggestion-commit'), batch.size === 0);
    toggle(block.querySelector('.damast-suggestion-add-batch'), !inBatch);
    toggle(block.querySelector('.damast-suggestion-remove-batch'), inBatch);
    toggle(block.querySelector('.damast-suggestion-commit-batch'), batch.size > 0);
    block.querySelector('.damast-suggestion-count').textContent = String(batch.size);
  }
}

function openForm(block, ids) {
  pending = ids;
  for (const form of document.querySelectorAll('.damast-suggestion-form')) toggle(form, false);
  const form = block.querySelector('.damast-suggestion-form');
  toggle(form, true);
  form.querySelector('textarea').focus();
}

// A comment edited after the page loaded shows a stale diff; don't commit what the user hasn't seen.
function isEdited(id) {
  const block = document.querySelector(`.damast-suggestion[data-comment-id="${id}"]`);
  const zone = block?.parentElement.querySelector('.edit-content-zone');
  return zone && zone.getAttribute('data-content-version') !== block.getAttribute('data-content-version');
}

async function submit(block) {
  const form = block.querySelector('.damast-suggestion-form');
  if (pending.some(isEdited)) {
    showErrorToast(block.getAttribute('data-edited-error'));
    return;
  }
  const data = new FormData();
  for (const id of pending) {
    data.append('comment_ids', id);
    // the server refuses comments edited since this page was loaded
    data.append('content_versions', document.querySelector(`.damast-suggestion[data-comment-id="${id}"]`).getAttribute('data-content-version'));
  }
  data.append('message', form.querySelector('textarea').value);
  const button = form.querySelector('.damast-suggestion-submit');
  button.classList.add('loading');
  try {
    const resp = await POST(block.getAttribute('data-apply-url'), {data});
    const json = await resp.json().catch(() => ({}));
    if (!resp.ok) {
      showErrorToast(json.errorMessage || resp.statusText);
      return;
    }
    window.location.reload();
  } finally {
    button.classList.remove('loading');
  }
}

// lineText returns the text of a line on the new side of the diff, or null if it isn't on the page.
function lineText(path, line) {
  const file = document.querySelector(`[data-path="${CSS.escape(path)}"]`);
  const num = file?.querySelector(`.lines-num-new[data-line-num="${line}"]`);
  const row = num?.closest('tr');
  if (!row) return null;
  const code = row.querySelector('.lines-code-new .code-inner') ?? row.querySelector('.lines-code .code-inner');
  return code ? code.textContent : null;
}

function fence(lines) {
  let f = '```';
  while (lines.some((l) => l.includes(f))) f += '`';
  return `${f}suggestion\n${lines.map((l) => `${l}\n`).join('')}${f}`;
}

function insertSuggestion(button) {
  const form = button.closest('form');
  const textarea = form?.querySelector('textarea.markdown-text-editor');
  if (!textarea) return;
  const value = (name) => form.querySelector(`input[name="${name}"]`)?.value ?? '';
  if (value('side') === 'previous') {
    showErrorToast(button.getAttribute('data-old-side-error'));
    return;
  }
  const start = parseInt(value('line'));
  const extra = parseInt(value('extra_lines_count')) || 0;
  const lines = [];
  for (let n = start; n <= start + extra; n++) {
    const text = lineText(value('path'), n);
    if (text === null) return;
    lines.push(text);
  }
  const before = textarea.value.slice(0, textarea.selectionStart);
  const block = `${before && !before.endsWith('\n') ? '\n' : ''}${fence(lines)}\n`;
  textarea.setRangeText(block, textarea.selectionStart, textarea.selectionEnd, 'end');
  textarea.dispatchEvent(new Event('input', {bubbles: true}));
  textarea.focus();
}

export function initDamastSuggestions() {
  document.addEventListener('click', (e) => {
    const suggest = e.target.closest('.damast-suggest');
    if (suggest) {
      e.preventDefault();
      insertSuggestion(suggest);
      return;
    }
    const block = e.target.closest('.damast-suggestion[data-apply-url]');
    if (!block) return;
    const id = block.getAttribute('data-comment-id');
    if (e.target.closest('.damast-suggestion-commit')) {
      openForm(block, [id]);
    } else if (e.target.closest('.damast-suggestion-commit-batch')) {
      openForm(block, Array.from(batch));
    } else if (e.target.closest('.damast-suggestion-add-batch')) {
      batch.add(id);
      render();
    } else if (e.target.closest('.damast-suggestion-remove-batch')) {
      batch.delete(id);
      render();
    } else if (e.target.closest('.damast-suggestion-cancel')) {
      toggle(block.querySelector('.damast-suggestion-form'), false);
    }
  });
  document.addEventListener('submit', (e) => {
    const form = e.target.closest('.damast-suggestion-form');
    if (!form) return;
    e.preventDefault();
    submit(form.closest('.damast-suggestion'));
  });
}

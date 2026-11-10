// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

import {initComboMarkdownEditor} from './comp/ComboMarkdownEditor.js';

// Markdown editors on the Security tab (advisory form and private discussion), and confirmations for
// destructive forms (deleting a comment).
export function initDamastSecurity() {
  for (const editor of document.querySelectorAll('.page-content.damast-security .combo-markdown-editor')) {
    initComboMarkdownEditor(editor);
  }
  for (const form of document.querySelectorAll('.page-content.damast-security form[data-damast-confirm]')) {
    form.addEventListener('submit', (e) => {
      if (!window.confirm(form.getAttribute('data-damast-confirm'))) e.preventDefault();
    });
  }
}

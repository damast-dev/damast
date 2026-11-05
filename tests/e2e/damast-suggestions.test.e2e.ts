// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// @watch start
// templates/repo/damast/**
// templates/repo/diff/comment_form.tmpl
// templates/shared/combomarkdowneditor.tmpl
// web_src/js/features/damast-suggestions.js
// web_src/css/damast.css
// @watch end

import {expect, type APIRequestContext} from '@playwright/test';
import {test, dynamic_id} from './utils_e2e.ts';

test.use({user: 'user2'});

const auth = (user: string) => ({Authorization: `Basic ${btoa(`${user}:password`)}`});

// createPR makes a new repository of user2 with a pull request that adds files.
async function createPR(request: APIRequestContext, files: Record<string, string>) {
  const name = dynamic_id();
  let resp = await request.post('/api/v1/user/repos', {headers: auth('user2'), data: {name, auto_init: true, default_branch: 'main'}});
  expect(resp.ok()).toBeTruthy();
  resp = await request.post(`/api/v1/repos/user2/${name}/contents`, {headers: auth('user2'), data: {
    branch: 'main', new_branch: 'feature', message: 'add files',
    files: Object.entries(files).map(([path, content]) => ({operation: 'create', path, content: btoa(content)})),
  }});
  expect(resp.ok()).toBeTruthy();
  resp = await request.post(`/api/v1/repos/user2/${name}/pulls`, {headers: auth('user2'), data: {base: 'main', head: 'feature', title: 'suggestions'}});
  expect(resp.ok()).toBeTruthy();
  return {name, index: (await resp.json()).number};
}

async function headFile(request: APIRequestContext, repo: string, path: string) {
  const resp = await request.get(`/api/v1/repos/user2/${repo}/raw/${path}?ref=feature`, {headers: auth('user2')});
  return resp.text();
}

test('Suggest button, then commit the suggestion', async ({page, request}) => {
  const {name, index} = await createPR(request, {'a.txt': 'one\ntwo\nthree\n'});
  await page.goto(`/user2/${name}/pulls/${index}/files`);

  await page.locator('[data-path="a.txt"] .add-code-comment[data-side="right"][data-idx="2"]').click();
  const form = page.locator('.comment-code-cloud form');
  const textarea = form.locator('textarea.markdown-text-editor');
  await expect(textarea).toBeVisible();
  await form.locator('.damast-suggest').click();
  await expect(textarea).toHaveValue('```suggestion\ntwo\n```\n');

  await textarea.fill('Shout it\n```suggestion\nTWO\n```');
  await form.locator('button.btn-add-single').click();

  const block = page.locator('.damast-suggestion');
  await expect(block.locator('.damast-suggestion-del code')).toHaveText('two');
  await expect(block.locator('.damast-suggestion-add code')).toHaveText('TWO');
  // the markdown code block is replaced by the mini diff
  await expect(page.locator('.comment-body pre:has(code.language-suggestion)')).toBeHidden();

  await block.locator('.damast-suggestion-commit').click();
  await expect(block.locator('.damast-suggestion-form textarea')).toHaveValue('Apply suggestions from code review');
  await block.locator('.damast-suggestion-submit').click();
  await page.waitForLoadState('load');
  await expect.poll(() => headFile(request, name, 'a.txt')).toBe('one\nTWO\nthree\n');
});

test('Batch of suggestions is one commit', async ({page, request}) => {
  const {name, index} = await createPR(request, {'a.txt': 'a\nb\n', 'b.txt': 'c\n'});
  // user1 reviews, so the commit credits them
  const resp = await request.post(`/api/v1/repos/user2/${name}/pulls/${index}/reviews`, {headers: auth('user1'), data: {
    event: 'COMMENT', body: 'review',
    comments: [
      {path: 'a.txt', new_position: 1, body: '```suggestion\nA\n```'},
      {path: 'b.txt', new_position: 1, body: '```suggestion\nC\n```'},
    ],
  }});
  expect(resp.ok()).toBeTruthy();

  await page.goto(`/user2/${name}/pulls/${index}/files`);
  const blocks = page.locator('.damast-suggestion');
  await expect(blocks).toHaveCount(2);
  await blocks.nth(0).locator('.damast-suggestion-add-batch').click();
  await expect(blocks.nth(0).locator('.damast-suggestion-remove-batch')).toBeVisible();
  await expect(blocks.nth(1).locator('.damast-suggestion-commit')).toBeHidden();
  await blocks.nth(1).locator('.damast-suggestion-add-batch').click();
  await expect(blocks.nth(1).locator('.damast-suggestion-count')).toHaveText('2');

  // remove and re-add works
  await blocks.nth(1).locator('.damast-suggestion-remove-batch').click();
  await expect(blocks.nth(0).locator('.damast-suggestion-count')).toHaveText('1');
  await blocks.nth(1).locator('.damast-suggestion-add-batch').click();

  await blocks.nth(0).locator('.damast-suggestion-commit-batch').click();
  await blocks.nth(0).locator('.damast-suggestion-form textarea').fill('Batch it');
  await blocks.nth(0).locator('.damast-suggestion-submit').click();
  await page.waitForLoadState('load');

  await expect.poll(() => headFile(request, name, 'a.txt')).toBe('A\nb\n');
  expect(await headFile(request, name, 'b.txt')).toBe('C\n');
  const commits = await (await request.get(`/api/v1/repos/user2/${name}/commits?sha=feature&limit=2`, {headers: auth('user2')})).json();
  expect(commits[0].commit.message).toMatch(/^Batch it\n\nCo-authored-by: .+ <.+>\n?$/);
  expect(commits[1].commit.message.trim()).toBe('add files');
});

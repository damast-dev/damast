// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// @watch start
// templates/repo/damast/pull_*.tmpl
// templates/repo/issue/new_form.tmpl
// templates/repo/issue/view_content/**
// web_src/js/features/repo-issue.js
// web_src/js/features/common-global.js
// @watch end

import {expect} from '@playwright/test';
import {test} from './utils_e2e.ts';

test.use({user: 'user2'});

test('Damast: create a draft PR, mark it ready, convert back', async ({page, request}, workerInfo) => {
  const headers = {Authorization: `Basic ${btoa('user2:password')}`};
  const repo = `damast-draft-${workerInfo.project.name.toLowerCase().replace(/\W/g, '')}-${Date.now()}`;
  let resp = await request.post('/api/v1/user/repos', {headers, data: {name: repo, auto_init: true, default_branch: 'main'}});
  expect(resp.status()).toBe(201);
  resp = await request.post(`/api/v1/repos/user2/${repo}/contents/feature.txt`, {
    headers, data: {content: btoa('feature'), branch: 'main', new_branch: 'feature', message: 'add feature'},
  });
  expect(resp.status()).toBe(201);

  try {
    await page.goto(`/user2/${repo}/compare/main...feature`);
    await page.locator('.show-form-container').getByText('New pull request').click();
    await page.locator('#issue_title').fill('Draft from the browser');

    // split button: the menu holds "Create draft pull request"
    const draftItem = page.locator('[data-test-name="damast-create-draft-pr"]');
    await expect(draftItem).toBeHidden();
    await page.locator('[data-test-name="damast-create-pr-menu"]').click();
    await expect(draftItem).toBeVisible();
    await draftItem.click();

    await expect(page).toHaveURL(new RegExp(`/user2/${repo}/pulls/1$`));
    await expect(page.locator('#issue-title-display h1')).toContainText('Draft from the browser');
    await expect(page.locator('#issue-title-display h1')).not.toContainText('WIP');
    await expect(page.locator('.issue-title-meta .issue-state-label')).toContainText('Draft');
    await expect(page.locator('#issue-title-editor input')).toHaveValue('WIP: Draft from the browser');

    // Ready for review in the merge box
    await page.locator('[data-test-name="damast-ready-for-review"]').click();
    await expect(page.locator('.issue-title-meta .issue-state-label')).toContainText('Open');
    await expect(page.locator('[data-test-name="damast-ready-for-review"]')).toHaveCount(0);

    // Convert to draft in the sidebar
    await page.locator('[data-test-name="damast-convert-to-draft"]').click();
    await expect(page.locator('.issue-title-meta .issue-state-label')).toContainText('Draft');
    await expect(page.locator('[data-test-name="damast-ready-for-review"]')).toBeVisible();

    // the list shows the badge and the title without prefix
    await page.goto(`/user2/${repo}/pulls`);
    const item = page.locator('#issue-list .flex-item').first();
    await expect(item.locator('a.issue-title')).toHaveText('Draft from the browser');
    await expect(item.locator('[data-test-name="damast-draft-badge"]')).toBeVisible();
  } finally {
    await request.delete(`/api/v1/repos/user2/${repo}`, {headers});
  }
});

test('Damast: plain "Create pull request" stays a normal PR', async ({page, request}, workerInfo) => {
  const headers = {Authorization: `Basic ${btoa('user2:password')}`};
  const repo = `damast-nodraft-${workerInfo.project.name.toLowerCase().replace(/\W/g, '')}-${Date.now()}`;
  let resp = await request.post('/api/v1/user/repos', {headers, data: {name: repo, auto_init: true, default_branch: 'main'}});
  expect(resp.status()).toBe(201);
  resp = await request.post(`/api/v1/repos/user2/${repo}/contents/feature.txt`, {
    headers, data: {content: btoa('feature'), branch: 'main', new_branch: 'feature', message: 'add feature'},
  });
  expect(resp.status()).toBe(201);

  try {
    await page.goto(`/user2/${repo}/compare/main...feature`);
    await page.locator('.show-form-container').getByText('New pull request').click();
    await page.locator('#issue_title').fill('Normal PR');
    await page.locator('[data-test-name="damast-create-pr"]').click();
    await expect(page).toHaveURL(new RegExp(`/user2/${repo}/pulls/1$`));
    await expect(page.locator('.issue-title-meta .issue-state-label')).toContainText('Open');
    await expect(page.locator('#issue-title-editor input')).toHaveValue('Normal PR');
  } finally {
    await request.delete(`/api/v1/repos/user2/${repo}`, {headers});
  }
});

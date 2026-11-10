// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// @watch start
// templates/repo/damast/security/**
// routers/web/repo/damast_security.go
// services/damast/security/**
// @watch end

import {expect} from '@playwright/test';
import {test, test_context, dynamic_id} from './utils_e2e.ts';

test.use({user: 'user2'});

test('Security advisory: create a draft and publish it', async ({page, browser}) => {
  const title = `E2E advisory ${dynamic_id()}`;

  await page.goto('/user2/repo1');
  // on narrow screens the tab sits in the "..." overflow menu
  const more = page.locator('.overflow-menu-button');
  if (await more.isVisible()) await more.click();
  await page.locator('a.item[href="/user2/repo1/security"]:visible').click();
  await expect(page).toHaveURL('/user2/repo1/security');
  await page.getByRole('link', {name: 'New draft advisory'}).click();
  await expect(page).toHaveURL('/user2/repo1/security/advisories/new');

  await page.fill('#damast-title', title);
  await page.fill('textarea[name=description]', 'Remote code execution in the **parser**.');
  await page.fill('#damast-cvss', 'CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H');
  await page.fill('input[name=package]', 'e2e-package');
  await page.fill('input[name=vulnerable]', '>= 1.0, < 1.4.2');
  await page.fill('input[name=patched]', '1.4.2');
  await page.click('#damast-advisory-submit');

  await expect(page.locator('#damast-advisory-title')).toHaveText(title);
  await expect(page.locator('#damast-advisory-private')).toBeVisible();
  await expect(page.locator('#damast-advisory-score')).toHaveText('9.8');
  await expect(page.locator('#damast-advisory-description strong')).toHaveText('parser');
  const url = page.url();

  // the private discussion
  await page.fill('#damast-advisory-comments textarea[name=content]', 'Fix is in review.');
  await page.locator('#damast-advisory-comments button.primary').click();
  await expect(page.locator('#damast-advisory-comments')).toContainText('Fix is in review.');

  // a draft is a 404 for anonymous visitors
  const anonymous = await (await test_context(browser)).newPage();
  expect((await anonymous.goto(url))?.status()).toBe(404);

  await page.getByRole('button', {name: 'Publish advisory'}).click();
  await expect(page.locator('#damast-advisory-private')).toHaveCount(0);
  await expect(page.locator('.ui.green.label')).toHaveText('Published');

  // published: public, without the private discussion
  expect((await anonymous.goto(url))?.status()).toBe(200);
  await expect(anonymous.locator('#damast-advisory-title')).toHaveText(title);
  await expect(anonymous.locator('#damast-advisory-comments')).toHaveCount(0);
  await anonymous.goto(new URL('/user2/repo1/security', url).toString());
  await expect(anonymous.locator('#damast-security-advisories')).toContainText(title);
});

test('Security advisory: edit and delete a comment, withdraw after confirming', async ({page}) => {
  const title = `E2E withdraw ${dynamic_id()}`;

  await page.goto('/user2/repo1/security/advisories/new');
  await page.fill('#damast-title', title);
  await page.fill('textarea[name=description]', 'Wrong report.');
  await page.click('#damast-advisory-submit');
  await expect(page.locator('#damast-advisory-title')).toHaveText(title);

  // comment, edit it, delete it (confirming)
  await page.fill('#damast-advisory-comments .combo-markdown-editor textarea[name=content]', 'First version.');
  await page.getByRole('button', {name: 'Comment'}).click();
  const comment = page.locator('.damast-advisory-comment');
  await expect(comment).toContainText('First version.');
  await comment.locator('summary').click();
  await comment.locator('textarea[name=content]').fill('Second version.');
  await comment.getByRole('button', {name: 'Save'}).click();
  await expect(comment.locator('.markup')).toHaveText('Second version.');

  await comment.locator('summary').click();
  page.once('dialog', (dialog) => dialog.dismiss());
  await comment.getByRole('button', {name: 'Remove'}).click();
  await expect(comment).toHaveCount(1); // dismissed: still there
  page.once('dialog', (dialog) => dialog.accept());
  await comment.getByRole('button', {name: 'Remove'}).click();
  await expect(comment).toHaveCount(0);

  await page.getByRole('button', {name: 'Publish advisory'}).click();
  await expect(page.locator('.ui.green.label')).toHaveText('Published');

  // withdrawing is final, so it asks first
  page.once('dialog', (dialog) => dialog.dismiss());
  await page.getByRole('button', {name: 'Withdraw advisory'}).click();
  await expect(page.locator('.ui.green.label')).toHaveText('Published');
  page.once('dialog', (dialog) => dialog.accept());
  await page.getByRole('button', {name: 'Withdraw advisory'}).click();
  await expect(page.locator('#damast-advisory-withdrawn')).toBeVisible();
  await expect(page.locator('#damast-advisory-actions')).toHaveCount(0);
});

// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// @ts-check
// Damast: stacked pull requests. Repository declared in tests/e2e/damast_declare_test.go:
// main <- api (#1) <- service (#2) <- ui (#3), service <- docs (#4, draft); main has moved on.
import {expect} from '@playwright/test';
import {test} from './utils_e2e.ts';

test.use({user: 'user2'});

// DAMAST_SHOTS=<dir> also writes the screenshots used in the README and on damast.dev
const shots = process.env.DAMAST_SHOTS;

test('Stack map, rebase stack and merge up to here', async ({page}) => {
  const response = await page.goto('/user2/stacked-prs/pulls/3');
  expect(response?.status()).toBe(200);

  const stack = page.locator('#damast-stack');
  await expect(stack).toBeVisible();
  await expect(stack).toContainText('Stack of 4 pull requests');
  const layers = stack.locator('.damast-stack-layer');
  await expect(layers).toHaveCount(4);
  await expect(layers.nth(0)).toContainText('Add the stack API');
  await expect(layers.nth(3)).toContainText('Document stacked pull requests');
  await expect(layers.nth(3)).toHaveAttribute('data-depth', '2', {timeout: 1000});
  await expect(stack.locator('[aria-current]')).toHaveAttribute('data-index', '3');
  await expect(stack.locator('.damast-stack-checks[data-state="success"]')).toHaveCount(2);
  await expect(stack.locator('.damast-stack-checks[data-state="pending"]')).toHaveCount(1);
  await expect(layers.nth(3).locator('.label')).toContainText('Draft');

  // the map follows the PR to its other tabs
  await page.goto('/user2/stacked-prs/pulls/3/files');
  await expect(page.locator('#damast-stack .damast-stack-layer')).toHaveCount(4);
  await page.goto('/user2/stacked-prs/pulls/3');

  if (shots) {
    const area = page.locator('.issue-title-header, #damast-stack');
    await page.locator('#damast-stack').scrollIntoViewIfNeeded();
    await page.screenshot({path: `${shots}/stack-light.png`, clip: await clipOf(area)});
    await page.emulateMedia({colorScheme: 'dark'});
    await page.reload();
    await page.screenshot({path: `${shots}/stack-dark.png`, clip: await clipOf(area)});
    await page.emulateMedia({colorScheme: 'light'});
    await page.setViewportSize({width: 390, height: 844});
    await page.reload();
    await page.screenshot({path: `${shots}/stack-mobile.png`, fullPage: false});
    await page.setViewportSize({width: 1280, height: 720});
    await page.reload();
  }

  // Rebase stack: every layer onto its parent, the bottom one onto main
  await page.locator('#damast-stack-rebase').click();
  await expect(page.locator('.flash-success')).toContainText('Rebased #1, #2, #3, #4.');

  // Merge up to here: #1, #2 and #3, after a confirmation
  await page.locator('#damast-stack-merge').click();
  const modal = page.locator('#damast-stack-merge-modal');
  await expect(modal).toBeVisible();
  await expect(modal).toContainText('#1, #2, #3');
  if (shots) await page.screenshot({path: `${shots}/stack-merge-modal.png`});
  await modal.locator('.ok.button').click();
  await expect(page.locator('.flash-success')).toContainText('Merged #1, #2, #3.');
  await expect(page.locator('.issue-state-label')).toContainText('Merged');

  // the draft layer was retargeted onto main and is on its own now
  await page.goto('/user2/stacked-prs/pulls/4');
  await expect(page.locator('#branch_target')).toContainText('main');
  await expect(page.locator('#damast-stack')).toHaveCount(0);
});

async function clipOf(locator) {
  const boxes = await Promise.all((await locator.all()).map((l) => l.boundingBox()));
  const x = Math.min(...boxes.map((b) => b.x)) - 16;
  const y = Math.min(...boxes.map((b) => b.y)) - 16;
  const right = Math.max(...boxes.map((b) => b.x + b.width)) + 16;
  const bottom = Math.max(...boxes.map((b) => b.y + b.height)) + 16;
  return {x: Math.max(0, x), y: Math.max(0, y), width: right - Math.max(0, x), height: bottom - Math.max(0, y)};
}

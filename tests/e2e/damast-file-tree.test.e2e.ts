// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// DAMAST: file tree sidebar of the code view (MAST-17)
// @watch start
// templates/repo/damast/file_tree.tmpl
// templates/repo/home.tmpl
// web_src/js/components/DamastFileTree.vue
// web_src/js/features/damast-file-tree.js
// web_src/css/damast.css
// routers/web/repo/damast_tree.go
// @watch end

import {expect, type Page} from '@playwright/test';
import {test} from './utils_e2e.ts';

// fixture repo user2/glob: a.txt, aaa.doc, x/b.txt, x/y/a.txt, x/y/z/a.txt
const base = '/user2/glob/src/branch/master';
const tree = (page: Page) => page.locator('#damast-file-tree');
const item = (page: Page, path: string) => tree(page).locator(`[role="treeitem"][title="${path}"]`);

test.describe('desktop', () => {
  test.skip(({isMobile}) => isMobile, 'desktop layout');

  test('deep file expands ancestors and highlights the entry', async ({page}) => {
    await page.goto(`${base}/x/y/z/a.txt`);
    await expect(tree(page)).toBeVisible();
    for (const dir of ['x', 'x/y', 'x/y/z']) {
      await expect(item(page, dir)).toHaveAttribute('aria-expanded', 'true');
    }
    await expect(item(page, 'x/y/z/a.txt')).toHaveAttribute('aria-selected', 'true');
    await expect(tree(page).locator('[aria-selected="true"]')).toHaveCount(1);
    // directories first, like the code view
    await expect(tree(page).locator('[aria-level="1"]').first()).toHaveAttribute('title', 'x');
  });

  test('navigates without a full reload and supports back/forward', async ({page}) => {
    await page.goto(`${base}/x/y/z/a.txt`);
    await expect(item(page, 'x/y/z/a.txt')).toHaveAttribute('aria-selected', 'true');
    await page.evaluate(() => document.body.setAttribute('data-damast-marker', '1')); // gone after a full page load

    await item(page, 'x/b.txt').locator('a').click();
    await expect(page).toHaveURL(`${base}/x/b.txt`);
    await expect(page).toHaveTitle(/glob\/x\/b\.txt/);
    await expect(page.locator('.repo-path')).toContainText('b.txt');
    await expect(page.locator('.file-view .lines-code')).toBeVisible();
    await expect(item(page, 'x/b.txt')).toHaveAttribute('aria-selected', 'true');
    await expect(page.locator('body')).toHaveAttribute('data-damast-marker', '1');

    // line selection works on swapped content
    await page.locator('.file-view .lines-num span[id="L1"]').click();
    await expect(page).toHaveURL(`${base}/x/b.txt#L1`);
    await expect(page.locator('.file-view tr.active')).toHaveCount(1);

    // a directory swaps in the file list, the branch dropdown links to the new path
    await item(page, 'x/y').locator('a').click();
    await expect(page).toHaveURL(`${base}/x/y`);
    await expect(page.locator('#repo-files-table')).toContainText('a.txt');
    await expect(page.locator('body')).toHaveAttribute('data-damast-marker', '1');

    await page.goBack();
    await expect(page).toHaveURL(`${base}/x/b.txt#L1`);
    await expect(page.locator('.repo-path')).toContainText('b.txt');
    await expect(item(page, 'x/b.txt')).toHaveAttribute('aria-selected', 'true');
    await page.goBack();
    await expect(page).toHaveURL(`${base}/x/b.txt`);
    await page.goBack();
    await expect(page).toHaveURL(`${base}/x/y/z/a.txt`);
    await expect(page.locator('.repo-path')).toContainText('z');
    await expect(item(page, 'x/y/z/a.txt')).toHaveAttribute('aria-selected', 'true');
    await page.goForward();
    await expect(page).toHaveURL(`${base}/x/b.txt`);
    await expect(page.locator('body')).toHaveAttribute('data-damast-marker', '1');
  });

  test('branch dropdown follows the swapped path', async ({page}) => {
    await page.goto(`${base}/x/y/z/a.txt`);
    await item(page, 'x/y').locator('a').click();
    await expect(page).toHaveURL(`${base}/x/y`);
    await page.locator('.branch-dropdown-button').click();
    await page.locator('.js-branch-tag-selector .menu .item').filter({hasText: 'master'}).first().click();
    await expect(page).toHaveURL(`${base}/x/y`);
    await expect(page.locator('#repo-files-table')).toContainText('a.txt');
  });

  test('keyboard navigation', async ({page}) => {
    await page.goto(`${base}/x/y/z/a.txt`);
    await item(page, 'x/y/z/a.txt').focus();
    await page.keyboard.press('ArrowLeft'); // to parent
    await expect(item(page, 'x/y/z')).toBeFocused();
    await page.keyboard.press('ArrowLeft'); // collapse
    await expect(item(page, 'x/y/z')).toHaveAttribute('aria-expanded', 'false');
    await page.keyboard.press('ArrowRight'); // expand
    await expect(item(page, 'x/y/z')).toHaveAttribute('aria-expanded', 'true');
    await page.keyboard.press('ArrowDown');
    await expect(item(page, 'x/y/z/a.txt')).toBeFocused();
    await page.keyboard.press('ArrowDown');
    await expect(item(page, 'x/y/a.txt')).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(`${base}/x/y/a.txt`);
  });

  test('lazy-loads folders and keeps them open across reloads', async ({page}) => {
    await page.goto(base);
    // repository root: hidden by default
    await expect(tree(page)).toBeHidden();
    await page.locator('.damast-file-tree-toggle').click();
    await expect(tree(page)).toBeVisible();
    await expect(item(page, 'x/b.txt')).toHaveCount(0);
    await item(page, 'x').locator('.damast-file-tree-chevron').click();
    await expect(item(page, 'x/b.txt')).toBeVisible();

    await page.reload();
    await expect(tree(page)).toBeVisible();
    await expect(item(page, 'x/b.txt')).toBeVisible();

    await page.locator('.damast-file-tree-toggle').click();
    await expect(tree(page)).toBeHidden();
    await page.goto(`${base}/x/b.txt`);
    await expect(tree(page)).toBeHidden();
    await page.locator('.damast-file-tree-toggle').click();
    await expect(item(page, 'x/b.txt')).toHaveAttribute('aria-selected', 'true');
  });
});

test.describe('mobile', () => {
  test.skip(({isMobile}) => !isMobile, 'mobile layout');

  test('hidden by default, opens on demand', async ({page}) => {
    await page.goto(`${base}/x/y/z/a.txt`);
    await expect(tree(page)).toBeHidden();
    await page.locator('.damast-file-tree-toggle').click();
    await expect(item(page, 'x/y/z/a.txt')).toHaveAttribute('aria-selected', 'true');
    await item(page, 'x/b.txt').locator('a').click();
    await expect(page).toHaveURL(`${base}/x/b.txt`);
    await expect(tree(page)).toBeHidden();
  });
});

// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// @watch start
// web_src/css/themes/theme-damast-*.css
// modules/setting/ui.go
// @watch end

import {expect} from '@playwright/test';
import {test} from './utils_e2e.ts';
import {accessibilityCheck} from './shared/accessibility.ts';

test.use({user: 'user2'});

const primaryBg = (page) => page.locator('.ui.primary.button').first().evaluate((el) => getComputedStyle(el).backgroundColor);

for (const [path, button] of [['/user2/repo1/issues/new', 'Create issue'], ['/user/settings', 'Update profile']]) {
  test(`Damast green on ${path}`, async ({page}) => {
    await page.goto(path);
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'damast-auto');
    await expect(page.getByRole('button', {name: button})).toBeVisible();

    await page.emulateMedia({colorScheme: 'light'});
    // poll: buttons fade between colours
    await expect.poll(() => primaryBg(page)).toBe('rgb(26, 127, 55)'); // #1a7f37
    await page.emulateMedia({colorScheme: 'dark'});
    await expect.poll(() => primaryBg(page)).toBe('rgb(63, 185, 80)'); // #3fb950

    // contrast of text on the green buttons, links and menus, in light and dark mode
    await page.emulateMedia({colorScheme: 'light'});
    await accessibilityCheck({page}, [], [], [], ['color-contrast']);
  });
}

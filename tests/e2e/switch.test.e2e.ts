// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// @watch start
// web_src/css/modules/switch.css
// web_src/css/modules/button.css
// web_src/css/themes/**
// @watch end

import {expect, type Page} from '@playwright/test';
import {test} from './utils_e2e.ts';

test.describe('Switch CSS properties', () => {
  const noBg = 'rgba(0, 0, 0, 0)';
  const activeBg = 'rgb(226, 226, 229)';
  const hoverBg = 'rgba(228, 228, 228, 0.667)';

  const normalMargin = '0px';
  const normalPadding = '15.75px';

  const specialLeftMargin = '-4px';
  const specialPadding = '19.75px';

  async function evaluateSwitchItem(page: Page, selector: string, hover, isActive: boolean, marginLeft, marginRight, paddingLeft, paddingRight: string, itemHeight: number) {
    const item = page.locator(selector);

    if (hover) await item.hover();

    const cs = await item.evaluate((el) => {
      // In Firefox getComputedStyle is undefined if returned from evaluate
      const s = getComputedStyle(el);
      return {
        backgroundColor: s.backgroundColor,
        marginLeft: s.marginLeft,
        marginRight: s.marginRight,
        paddingLeft: s.paddingLeft,
        paddingRight: s.paddingRight,
      };
    });
    expect(cs.marginLeft).toBe(marginLeft);
    expect(cs.marginRight).toBe(marginRight);
    expect(cs.paddingLeft).toBe(paddingLeft);
    expect(cs.paddingRight).toBe(paddingRight);

    if (isActive) {
      await expect(item).toHaveClass(/active/);

      // Active item has active background color regardless of `hover`
      expect(cs.backgroundColor).toBe(activeBg);
    } else {
      await expect(item).not.toHaveClass(/active/);

      // When hovering, `getComputedStyle` returns random `backgroundColor` values
      // because of transition. `toHaveCSS` is reliable
      if (hover) {
        // Verify that inactive item changes it's background color on hover
        await expect(item).toHaveCSS('background-color', hoverBg);
      } else {
        // Verify that inactive item doesn't have a background color
        await expect(item).toHaveCSS('background-color', noBg);
      }
    }

    expect((await item.boundingBox()).height).toBeCloseTo(itemHeight, 1);

    // Reset hover
    if (hover) await page.locator('#navbar-logo').hover();
  }

  // Subtest for areas that can be evaluated without JS
  test('No JS', async ({browser}) => {
    const context = await browser.newContext({javaScriptEnabled: false});
    const page = await context.newPage();

    const itemHeight = await page.evaluate(() => window.matchMedia('(pointer: coarse)').matches) ? 38 : 34;

    await page.goto('/user2/repo1/pulls');

    await expect(async () => {
      await Promise.all([
        evaluateSwitchItem(page, '#issue-filters .switch > .item:nth-child(1)', false, true, normalMargin, normalMargin, normalPadding, normalPadding, itemHeight),
        evaluateSwitchItem(page, '#issue-filters .switch > .item:nth-child(2)', false, false, specialLeftMargin, normalMargin, specialPadding, normalPadding, itemHeight),
        evaluateSwitchItem(page, '#issue-filters .switch > .item:nth-child(3)', false, false, normalMargin, normalMargin, normalPadding, normalPadding, itemHeight),
      ]);
    }).toPass();

    await page.goto('/user2/repo1/pulls?state=closed');

    await expect(async () => {
      await Promise.all([
        evaluateSwitchItem(page, '#issue-filters .switch > .item:nth-child(1)', false, false, normalMargin, specialLeftMargin, normalPadding, specialPadding, itemHeight),
        evaluateSwitchItem(page, '#issue-filters .switch > .item:nth-child(2)', false, true, normalMargin, normalMargin, normalPadding, normalPadding, itemHeight),
        evaluateSwitchItem(page, '#issue-filters .switch > .item:nth-child(3)', false, false, specialLeftMargin, normalMargin, specialPadding, normalPadding, itemHeight),
      ]);
    }).toPass();

    await page.goto('/user2/repo1/pulls?state=all');

    await expect(async () => {
      await Promise.all([
        evaluateSwitchItem(page, '#issue-filters .switch > .item:nth-child(1)', false, false, normalMargin, normalMargin, normalPadding, normalPadding, itemHeight),
        evaluateSwitchItem(page, '#issue-filters .switch > .item:nth-child(2)', false, false, normalMargin, specialLeftMargin, normalPadding, specialPadding, itemHeight),
        evaluateSwitchItem(page, '#issue-filters .switch > .item:nth-child(3)', false, true, normalMargin, normalMargin, normalPadding, normalPadding, itemHeight),
      ]);
    }).toPass();

    // Check colors on hover synchronously - can only hover one item at a time
    await expect(async () => {
      await evaluateSwitchItem(page, '#issue-filters .switch > .item:nth-child(1)', true, false, normalMargin, normalMargin, normalPadding, normalPadding, itemHeight);
      await evaluateSwitchItem(page, '#issue-filters .switch > .item:nth-child(2)', true, false, normalMargin, specialLeftMargin, normalPadding, specialPadding, itemHeight);
      await evaluateSwitchItem(page, '#issue-filters .switch > .item:nth-child(3)', true, true, normalMargin, normalMargin, normalPadding, normalPadding, itemHeight);
    }).toPass();
  });

  // Subtest for areas that can't be reached without JS
  test.describe('With JS', () => {
    test.use({user: 'user2'});

    test('PR review box', async ({page}) => {
      // Go to files tab of a reviewable pull request
      await page.goto('/user2/repo1/pulls/5/files');

      // Open review box
      await page.locator('#review-box .js-btn-review').click();

      // Markdown editor has a special rule for a shorter switch
      const itemHeight = 28;

      await expect(async () => {
        await Promise.all([
          evaluateSwitchItem(page, '.review-box-panel .switch > .item:nth-child(1)', false, true, normalMargin, normalMargin, normalPadding, normalPadding, itemHeight),
          evaluateSwitchItem(page, '.review-box-panel .switch > .item:nth-child(2)', false, false, specialLeftMargin, normalMargin, specialPadding, normalPadding, itemHeight),
        ]);
      }).toPass();
    });

    test('Notifications page', async ({page}) => {
      // Test counter contrast boost in active and :hover items
      const labelBgNormal = 'rgba(202, 202, 202, 0.482)';
      const labelBgContrast = 'rgb(202, 202, 202)';
      const counter = page.locator('a.item[href="/notifications?q=unread"] > .ui.label');

      // On Unread tab (item with counter is active)
      await page.goto('/notifications?q=unread');

      // * not hovering => boosted because .active
      expect(await counter.evaluate((el) => getComputedStyle(el).backgroundColor)).toBe(labelBgContrast);

      // * hoveing => boosted
      await page.locator('a.item[href="/notifications?q=unread"]').hover();
      expect(await counter.evaluate((el) => getComputedStyle(el).backgroundColor)).toBe(labelBgContrast);

      // On Read tab (item with counter is inactive)
      await page.goto('/notifications?q=read');

      // * not hovering => normal
      await page.locator('#navbar-logo').hover();
      expect(await counter.evaluate((el) => getComputedStyle(el).backgroundColor)).toBe(labelBgNormal);

      // * hoveing => boosted
      await page.locator('a.item[href="/notifications?q=unread"]').hover();
      expect(await counter.evaluate((el) => getComputedStyle(el).backgroundColor)).toBe(labelBgContrast);
    });
  });
});

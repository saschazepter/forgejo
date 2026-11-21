// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// @watch start
// templates/demo/modal.tmpl
// templates/repo/editor/edit.tmpl
// templates/repo/editor/patch.tmpl
// web_src/js/features/repo-editor.js
// web_src/js/modules/modal.ts
// web_src/css/modules/dialog.css
// @watch end

import {expect} from '@playwright/test';
import {dynamic_id, test} from './utils_e2e.ts';
import {testModalClosure} from './shared/modals.ts';
import {screenshot} from './shared/screenshots.ts';

test.use({user: 'user2'});

test('Dialog modal', async ({page}) => {
  let response = await page.goto('/user2/repo1/_new/master', {waitUntil: 'domcontentloaded'});
  expect(response?.status()).toBe(200);

  const filename = `${dynamic_id()}.md`;

  await page.getByPlaceholder('Name your file…').fill(filename);
  await page.locator('.cm-content').click();
  await page.keyboard.type('Hi, nice to meet you. Can I talk about ');

  await page.locator('.quick-pull-choice input[value="direct"]').click();
  await page.getByRole('button', {name: 'Commit changes'}).click();
  await expect(page).toHaveURL(`/user2/repo1/src/branch/master/${filename}`);

  response = await page.goto(`/user2/repo1/_edit/master/${filename}`, {waitUntil: 'domcontentloaded'});
  expect(response?.status()).toBe(200);

  await page.locator('.cm-content').click();
  await page.keyboard.press('ControlOrMeta+A');
  await page.keyboard.press('Backspace');

  await page.locator('#commit-button').click();
  await screenshot(page);
  await expect(page.locator('#edit-empty-content-modal')).toBeVisible();

  await page.locator('#edit-empty-content-modal .cancel').click();
  await expect(page.locator('#edit-empty-content-modal')).toBeHidden();

  await page.locator('#commit-button').click();
  await page.locator('#edit-empty-content-modal .ok').click();
  await expect(page).toHaveURL(`/user2/repo1/src/branch/master/${filename}`);
});

for (const run of [
  {title: 'JS off', useJs: false},
  {title: 'JS on', useJs: true},
]) {
  test.describe(`Dialog modal (${run.title})`, () => {
    test.use({javaScriptEnabled: run.useJs});

    test('width', async ({page, isMobile}) => {
      await page.goto('/-/demo/modal');

      // Open modal with short content
      const shortModal = page.locator('#short-modal');
      await expect(shortModal).toBeHidden();
      await page.locator('button[command="show-modal"][commandfor="short-modal"]').click();
      await expect(shortModal).toBeVisible();

      // Check it's width
      let width = Math.round((await shortModal.boundingBox()).width);
      if (isMobile) {
        // Bound by viewport width
        expect(width).toBeLessThan(400);
      } else {
        // Bound by min-width
        expect(width).toBe(400);
      }

      // Open modal with medium sized content
      await shortModal.locator('button[command="close"]').click();
      const mediumModal = page.locator('#medium-modal');
      await expect(mediumModal).toBeHidden();
      await page.locator('button[command="show-modal"][commandfor="medium-modal"]').click();
      await expect(mediumModal).toBeVisible();

      // Check it's width
      width = Math.round((await mediumModal.boundingBox()).width);
      if (isMobile) {
        // Bound by viewport width
        expect(width).toBeLessThan(400);
      } else {
        // Not bound by min-width or max-width
        expect(width).toBeLessThan(800);
        expect(width).toBeGreaterThan(400);
      }

      // Open modal with long content
      await mediumModal.locator('button[command="close"]').click();
      const longModal = page.locator('#long-modal');
      await expect(longModal).toBeHidden();
      await page.locator('button[command="show-modal"][commandfor="long-modal"]').click();
      await expect(longModal).toBeVisible();

      // Check it's width
      width = Math.round((await longModal.boundingBox()).width);
      if (isMobile) {
        // Bound by viewport width
        expect(width).toBeLessThan(400);
      } else {
        // Bound by max-width
        expect(width).toBe(800);
      }
    });

    for (const id of ['short-modal', 'medium-modal', 'long-modal']) {
      test.describe(id, () => {
        test.beforeEach(async ({page}) => {
          await page.goto('/-/demo/modal');
        });

        testModalClosure(
          (page) => page.locator(`#${id}`),
          (page) => page.locator(`button[command="show-modal"][commandfor="${id}"]`),
          'Close',
        );
      });
    }

    const sizeCases = [208, 310, 400, 600] as const;
    for (const width of sizeCases) {
      for (const height of sizeCases) {
        test(`all content scrollable in ${width}x${height} px viewport`, async ({page}) => {
          await page.setViewportSize({width, height});
          await page.goto('/-/demo/modal');

          // Open modal with long content
          const longModal = page.locator('#long-modal');
          await expect(longModal).toBeHidden();
          await page.locator('button[command="show-modal"][commandfor="long-modal"]').click();
          await expect(longModal).toBeVisible();

          // Make sure the heading is reachable
          const header = page.locator('header').filter({hasText: 'Long modal'});
          await header.scrollIntoViewIfNeeded();
          await expect(header).toBeVisible();
          await expect(header).toBeInViewport({ratio: 1});

          // Make sure the Cancel button is reachable
          const cancelButton = longModal.locator('button[command="close"]');
          await longModal.evaluate((v) => v.scrollTo(0, v.scrollHeight)); // scroll to bottom, even if button is partly visible
          await expect(header).toBeVisible();
          await expect(cancelButton).toBeInViewport({ratio: 1});
        });
      }
    }
  });
}

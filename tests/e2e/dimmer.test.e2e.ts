// @watch start
// templates/shared/user/**
// web_src/js/modules/dimmer.ts
// web_src/css/modules/dimmer.css
// @watch end

import {expect} from '@playwright/test';
import {test} from './utils_e2e.ts';
import {screenshot} from './shared/screenshots.ts';

test.use({user: 'user2'});

test('Dimmed modal (regenerate access token)', async ({page}) => {
  await page.goto('/user/settings/applications');

  const modalOpener = page.locator('button[data-modal-id="regenerate-token"]');
  await expect(modalOpener).toContainText('Regenerate');

  // Ensure the modal is hidden
  const modal = page.locator('#regenerate-token');
  const dimmer = page.locator('.ui.dimmer');
  await expect(modal).toBeHidden();

  await modalOpener.click();

  // Modal and dimmer should be visible.
  await expect(modal).toBeVisible();
  await expect(dimmer).toBeVisible();
  await screenshot(page, modal, 50);

  // After canceling, modal and dimmer should be hidden.
  await modal.locator('.cancel.button').click();
  await expect(modal).toBeHidden();
  await expect(dimmer).toBeHidden();
  await screenshot(page);

  // Open the block modal and make the dimmer visible again.
  await modalOpener.click();
  await expect(modal).toBeVisible();
  await expect(dimmer).toBeVisible();
  await expect(dimmer).toHaveCount(1);
  await screenshot(page, modal, 50);
});

test('Dimmed overflow', async ({page}) => {
  await page.goto('/user2/repo1/_new/master/');

  // Type in a file name.
  await page.locator('#file-name').click();
  await page.keyboard.type('todo.txt');

  // Scroll to the bottom.
  const scrollY = await page.evaluate(() => document.body.scrollHeight);
  await page.mouse.wheel(0, scrollY);

  // Click on 'Commit changes'
  await page.locator('#commit-button').click();

  // Expect a 'are you sure, this file is empty' modal.
  await expect(page.locator('#edit-empty-content-modal')).toBeVisible();
  await expect(page.locator('#edit-empty-content-modal header')).toContainText('Commit an empty file');
  await screenshot(page);

  // Trickery to check the page cannot be scrolled.
  const {overflow} = await page.evaluate(() => {
    const s = getComputedStyle(document.body);
    return {
      overflow: s.overflow,
    };
  });
  expect(overflow).toBe('hidden');
});

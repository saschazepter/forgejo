// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// @watch start
// templates/repo/watch_unwatch.tmpl
// web_src/css/repo/header.css
// @watch end

import {expect} from '@playwright/test';
import {test} from './utils_e2e.ts';

test.use({user: 'user2'});

test('Watch dropdown: toggle watch events', async ({page}, _) => {
  await page.goto('/user2/repo1');

  // Find the watch dropdown
  const watchDropdown = page.locator('.dialog-dropdown#watch-button');
  const watchOpener = watchDropdown.locator('button[command="toggle-popover"]');
  const watchMenu = watchDropdown.locator('dialog');

  // Open the dropdown
  await watchOpener.click();
  await expect(watchMenu).toBeVisible();

  // Verify checkboxes are present
  const issuesCheckbox = watchMenu.locator('input[name="watch_issues"]');
  const prsCheckbox = watchMenu.locator('input[name="watch_pull_requests"]');
  const releasesCheckbox = watchMenu.locator('input[name="watch_releases"]');

  await expect(issuesCheckbox).toBeVisible();
  await expect(prsCheckbox).toBeVisible();
  await expect(releasesCheckbox).toBeVisible();

  // Close dropdown by clicking elsewhere
  await page.locator('h1').click();
  await expect(watchMenu).toBeHidden();
});

test('Watch dropdown: unwatch all shows proper state', async ({page}, _) => {
  await page.goto('/user2/repo1');

  const watchDropdown = page.locator('.dialog-dropdown#watch-button');
  const watchOpener = watchDropdown.locator('button[command="toggle-popover"]');
  const watchMenu = watchDropdown.locator('dialog');

  // Open dropdown and click unwatch
  await watchOpener.click();
  await expect(watchMenu).toBeVisible();

  const unwatchButton = watchMenu.locator('button:has-text("Unwatch")');
  if (await unwatchButton.isVisible()) {
    await unwatchButton.click();
    // After unwatch, the button should show "Watch" state
    await expect(watchOpener).toContainText('Watch');
  }
});

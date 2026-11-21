// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// @watch start
// templates/repo/issue/labels/**
// web_src/js/features/comp/LabelEdit.js
// @watch end

import {expect} from '@playwright/test';
import {test, dynamic_id} from './utils_e2e.ts';
import {screenshot} from './shared/screenshots.ts';

test.use({user: 'user2'});

test('New label', async ({page}) => {
  const response = await page.goto('/user2/repo1/labels');
  expect(response?.status()).toBe(200);

  await page.getByRole('button', {name: 'New label'}).click();
  await expect(page.locator('#new-label-modal')).toBeVisible();
  await screenshot(page, page.locator('#new-label-modal'));

  const labelName = dynamic_id();
  await page.getByRole('textbox', {name: 'Label name'}).fill(labelName);
  await page.getByRole('button', {name: 'Create label'}).click();

  await expect(page.locator('.label-title').filter({hasText: labelName})).toBeVisible();
});

test('Edit label', async ({page}) => {
  const response = await page.goto('/user2/repo1/labels');
  expect(response?.status()).toBe(200);

  await page.getByText('Edit').first().click();
  await expect(page.locator('#edit-label-modal')).toBeVisible();
  await screenshot(page, page.locator('#edit-label-modal'));

  const labelName = dynamic_id();
  await page.getByRole('textbox', {name: 'Label name'}).fill(labelName);
  await page.getByRole('button', {name: 'Save'}).click();

  await expect(page.locator('.label-title').filter({hasText: labelName})).toBeVisible();
});

test('New label after a failed validation', async ({page}) => {
  // for issue https://codeberg.org/forgejo/forgejo/issues/11842
  const response = await page.goto('/user2/repo1/labels');
  expect(response?.status()).toBe(200);

  await page.getByRole('button', {name: 'New label'}).click();
  await expect(page.locator('#new-label-modal')).toBeVisible();

  // attempt to submit the form without having filled it first
  await page.getByRole('button', {name: 'Create label'}).click();
  await screenshot(page, page.locator('#new-label-modal'));

  // then fill the form and submit it again
  const labelName = dynamic_id();
  await page.getByRole('textbox', {name: 'Label name'}).fill(labelName);
  await page.getByRole('button', {name: 'Create label'}).click();

  await expect(page.locator('.label-title').filter({hasText: labelName})).toBeVisible();
});

// @watch start
// web_src/js/features/comp/**
// web_src/js/features/repo-**
// templates/repo/issue/view_content/**
// @watch end

import {expect} from '@playwright/test';
import {test} from './utils_e2e.ts';
import {screenshot} from './shared/screenshots.ts';

test.use({user: 'user2'});

test('Issue timetracking', async ({page}) => {
  await page.goto('/user2/repo1/issues/new');

  // Create temporary issue.
  await page.getByPlaceholder('Title').fill('Just a title');
  await page.getByPlaceholder('Leave a comment').fill('Hi, have you considered using a rotating fish as logo?');
  await page.getByRole('button', {name: 'Create issue'}).click();
  await expect(page).toHaveURL(/\/user2\/repo1\/issues\/\d+$/);

  // Manually add time to the time tracker.
  await page.getByRole('button', {name: 'Add time'}).click();
  await page.getByPlaceholder('Hours').fill('5');
  await page.getByPlaceholder('Minutes').fill('32');
  await page.getByRole('button', {name: 'Add time', exact: true}).click();

  // Verify this was added in the timeline.
  await expect(page.locator('.ui.timeline')).toContainText('added spent time');
  await expect(page.locator('.ui.timeline')).toContainText('5 hours 32 minutes');

  // Verify it is shown in the issue sidebar
  await expect(page.locator('.issue-content-right .comments')).toContainText('Total time spent: 5 hours 32 minutes');

  await screenshot(page);

  // Delete the added time.
  await page.getByRole('button', {name: 'Delete this time log'}).click();
  await page.getByRole('button', {name: 'Yes'}).click();

  // Verify this was removed in the timeline.
  await expect(page.locator('.ui.timeline')).toContainText('deleted spent time');
  await expect(page.locator('.ui.timeline')).toContainText('- 5 hours 32 minutes');

  // Delete the issue.
  await page.getByRole('button', {name: 'Delete'}).click();
  await page.getByRole('button', {name: 'Yes'}).click();
  await expect(page).toHaveURL('/user2/repo1/issues');
});

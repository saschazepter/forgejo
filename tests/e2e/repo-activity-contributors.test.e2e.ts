// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// @watch start
// web_src/js/features/contributors.js
// web_src/js/components/RepoContributors.vue
// templates/repo/*.tmpl
// @watch end

import {expect} from '@playwright/test';
import {test} from './utils_e2e.ts';

test.describe('Contributor graph', () => {
  test('navigate to user commits', async ({page}) => {
    await page.goto('/user2/commits_search_test/activity/contributors');
    await page.getByRole('link', {name: '2 Commits'}).click();
    await expect(page.getByRole('cell', {name: 'Bob'})).toHaveCount(2);
  });

  test('default heading omits future', async ({page}) => {
    await page.goto('/user2/commits_search_test/activity/contributors');

    /* Verify that Vue has access to i18n string */
    const header = page.getByRole('heading', {'level': 1});
    await header.waitFor();
    await expect(header).toContainText(/Contributors since \d+ years ago/);
  });

  test('heading changes with new time scope', async ({page}) => {
    await page.goto('/user2/commits_search_test/activity/contributors');
    const graph = page.locator('.main-graph');
    const graphBox = await graph.boundingBox();
    await graph.dragTo(graph, {
      // drag from near start to about halfway through
      sourcePosition: {x: graphBox.width / 4, y: graphBox.height / 2},
      targetPosition: {x: graphBox.width / 2, y: graphBox.height / 2},
    });

    /* Verify that Vue has access to i18n string */
    const header = page.getByRole('heading', {'level': 1});
    await header.waitFor();
    await expect(header).toContainText(/Contributors between \d+ years ago and \d+ years ago/);
  });
});

test('Code frequency', async ({page}) => {
  await page.goto('/user2/commits_search_test/activity/code-frequency');

  /* Verify that Vue has access to i18n string */
  const header = page.getByRole('heading', {'level': 1});
  await header.waitFor();
  await expect(header).toContainText('Code frequency over the history of user2/commits_search_test');
});

test('Recent commits', async ({page}) => {
  await page.goto('/user2/commits_search_test/activity/recent-commits');

  /* Verify that Vue has access to i18n string */
  const header = page.getByRole('heading', {'level': 1});
  await header.waitFor();
  await expect(header).toContainText('Number of commits in the past year');
});

// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// @watch start
// services/gitdiff/**
// templates/repo/view_file.tmpl
// web_src/css/repo.css
// web_src/css/repo/file-view.css
// web_src/js/features/repo-code.js
// web_src/js/features/repo-unicode-escape.js
// @watch end

import {expect, type Page} from '@playwright/test';
import {save_visual, test} from './utils_e2e.ts';
import {accessibilityCheck} from './shared/accessibility.ts';

async function assertSelectedLines(page: Page, nums: string[]) {
  const pageAssertions = async () => {
    expect(
      await Promise.all((await page.locator('tr.active [data-line-number]').all()).map((line) => line.getAttribute('data-line-number'))),
    )
      .toStrictEqual(nums);

    // the first line selected has an action button
    if (nums.length > 0) await expect(page.locator(`.lines-num:has(#L${nums[0]}) .code-line-button`)).toBeVisible();
  };

  await pageAssertions();

  // URL has the expected state
  expect(new URL(page.url()).hash)
    .toEqual(nums.length === 0 ? '' : nums.length === 1 ? `#L${nums[0]}` : `#L${nums[0]}-L${nums.at(-1)}`);

  // test selection restored from URL hash
  await page.reload();
  return pageAssertions();
}

test('Line Range Selection', async ({page}) => {
  const filePath = '/user2/repo1/src/branch/master/README.md?display=source';

  const response = await page.goto(filePath);
  expect(response?.status()).toBe(200);

  await assertSelectedLines(page, []);
  await page.locator('span#L1').click();
  await assertSelectedLines(page, ['1']);
  await page.locator('span#L3').click({modifiers: ['Shift']});
  await assertSelectedLines(page, ['1', '2', '3']);
  await page.locator('span#L2').click();
  await assertSelectedLines(page, ['2']);
  await page.locator('span#L1').click({modifiers: ['Shift']});
  await assertSelectedLines(page, ['1', '2']);

  // out-of-bounds end line
  await page.goto(`${filePath}#L1-L100`);
  await assertSelectedLines(page, ['1', '2', '3']);
  await save_visual(page);
});

test('Readable diff', async ({page}, workerInfo) => {
  // remove this when the test covers more (e.g. accessibility scans or interactive behaviour)
  test.skip(workerInfo.project.name !== 'firefox', 'This currently only tests the backend-generated HTML code and it is not necessary to test with multiple browsers.');
  const expectedDiffs = [
    {id: 'testfile-2', removed: 'e', added: 'a'},
    {id: 'testfile-3', removed: 'allo', added: 'ola'},
    {id: 'testfile-4', removed: 'hola', added: 'native'},
    {id: 'testfile-5', removed: 'native', added: 'ubuntu-latest'},
    {id: 'testfile-6', added: '- runs-on: '},
    {id: 'testfile-7', removed: 'ubuntu', added: 'debian'},
  ];
  for (const thisDiff of expectedDiffs) {
    const response = await page.goto('/user2/diff-test/commits/branch/main');
    expect(response?.status()).toBe(200); // Status OK
    await page.getByText(`Patch: ${thisDiff.id}`).click();
    if (thisDiff.removed) {
      await expect(page.getByText(thisDiff.removed, {exact: true})).toHaveClass(/removed-code/);
      await expect(page.getByText(thisDiff.removed, {exact: true})).toHaveCSS('background-color', 'rgb(252, 165, 165)');
    }
    if (thisDiff.added) {
      await expect(page.getByText(thisDiff.added, {exact: true})).toHaveClass(/added-code/);
      await expect(page.getByText(thisDiff.added, {exact: true})).toHaveCSS('background-color', 'rgb(134, 239, 172)');
    }
  }
  await save_visual(page);
});

test.describe('As authenticated user', () => {
  test.use({user: 'user2'});

  test('Username highlighted in commits', async ({page}) => {
    await page.goto('/user2/mentions-highlighted/commits/branch/main');
    // check first commit
    await page.getByRole('link', {name: 'A commit message which'}).click();
    await expect(page.getByRole('link', {name: '@user2'})).toHaveCSS('background-color', /(.*)/);
    await expect(page.getByRole('link', {name: '@user1'})).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)');
    await accessibilityCheck({page}, ['.commit-header'], [], []);
    await save_visual(page);
    // check second commit
    await page.goto('/user2/mentions-highlighted/commits/branch/main');
    await page.locator('tbody').getByRole('link', {name: 'Another commit which mentions'}).click();
    await expect(page.getByRole('link', {name: '@user2'})).toHaveCSS('background-color', /(.*)/);
    await expect(page.getByRole('link', {name: '@user1'})).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)');
    await accessibilityCheck({page}, ['.commit-header'], [], []);
    await save_visual(page);
  });
});

test('Unicode escape highlight', async ({page}) => {
  const unselectedBg = 'rgba(0, 0, 0, 0)';
  const selectedBg = 'rgb(255, 237, 213)';

  const response = await page.goto('/user2/unicode-escaping/src/branch/main/a-file');
  expect(response?.status()).toBe(200);

  await expect(page.locator('.unicode-escape-prompt')).toBeVisible();
  expect(await page.locator('.lines-num').evaluate((el) => getComputedStyle(el).backgroundColor)).toBe(unselectedBg);
  expect(await page.locator('.lines-escape').evaluate((el) => getComputedStyle(el).backgroundColor)).toBe(unselectedBg);
  expect(await page.locator('.lines-code').evaluate((el) => getComputedStyle(el).backgroundColor)).toBe(unselectedBg);

  await page.locator('#L1').click();
  expect(await page.locator('.lines-num').evaluate((el) => getComputedStyle(el).backgroundColor)).toBe(selectedBg);
  expect(await page.locator('.lines-escape').evaluate((el) => getComputedStyle(el).backgroundColor)).toBe(selectedBg);
  expect(await page.locator('.lines-code').evaluate((el) => getComputedStyle(el).backgroundColor)).toBe(selectedBg);

  await page.locator('.code-line-button').click();
  await expect(page.locator('.tippy-box .view_git_blame[href$="/a-file#L1"]')).toBeVisible();
  await expect(page.locator('.tippy-box .copy-line-permalink[data-url$="/a-file#L1"]')).toBeVisible();
});

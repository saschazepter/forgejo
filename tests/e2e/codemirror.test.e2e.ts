// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// @watch start
// templates/repo/editor/edit.tmpl
// web_src/css/features/codeeditor.css
// web_src/js/features/codeeditor.ts
// web_src/js/features/codemirror*.ts
// web_src/js/features/repo-editor.js
// web_src/js/features/repo-settings.js
// web_src/js/vendor/jquery.are-you-sure.js
// @watch end

import {expect, type Page} from '@playwright/test';
import {test} from './utils_e2e.ts';

test.use({user: 'user1'});

async function enterFilename(page: Page, filename: string) {
  const filenameInput = page.getByPlaceholder('Name your file…');
  await filenameInput.fill(filename);
}

async function pressEnter(page: Page) {
  await page.keyboard.press('Enter', {delay: 5});
}

async function type(page: Page, text: string) {
  await page.keyboard.type(text, {delay: 10});
}

async function validate(page: Page, expected: string) {
  await expect(async () => {
    const internal = await page.evaluate(() => Array.from(window.codeEditors)[0].state.doc.toString());
    expect(internal).toStrictEqual(expected);
  }).toPass();
  await expect(page.locator('#edit_area')).toHaveValue(expected);
}

test('New file editor', async ({page}) => {
  const response = await page.goto('/user2/repo1/_new/master', {waitUntil: 'domcontentloaded'});
  expect(response?.status()).toBe(200);

  await enterFilename(page, `f.txt`);

  const editor = page.locator('.cm-content');

  await editor.click();

  await type(page, 'This');
  await pressEnter(page);
  await validate(page, 'This\n');

  await type(page, 'is');
  await pressEnter(page);
  await validate(page, 'This\nis\n');

  await type(page, 'Frogejo!');
  await validate(page, 'This\nis\nFrogejo!');
});

test('New file with autocomplete and indent', async ({page}) => {
  const response = await page.goto('/user2/repo1/_new/master', {waitUntil: 'domcontentloaded'});
  expect(response?.status()).toBe(200);

  await enterFilename(page, 'f.html');

  const editor = page.locator('.cm-content');

  await expect(editor).toHaveAttribute('data-language', 'html', {timeout: 3000});

  await editor.click();
  await type(page, '<html>');
  await pressEnter(page);
  await validate(page, '<html>\n  \n</html>');

  await type(page, '<head>');
  await pressEnter(page);
  await validate(page, '<html>\n  <head>\n    \n  </head>\n</html>');

  await type(page, '<title>Frogejo is the future');
  await validate(page, '<html>\n  <head>\n    <title>Frogejo is the future</title>\n  </head>\n</html>');
});

test('Preview for markdown file', async ({page}) => {
  const response = await page.goto('/user2/repo1/_new/master?value=%23%20Frogejo', {waitUntil: 'domcontentloaded'});
  expect(response?.status()).toBe(200);

  await enterFilename(page, 'f.md');

  const editor = page.locator('.cm-content');
  const preview = page.locator('button[data-tab="preview"]');

  await expect(editor).toHaveAttribute('data-language', 'markdown', {timeout: 3000});

  await preview.click();
  await expect(preview).toHaveClass(/(^|\s)active(\s|$)/);
  await expect(page.getByRole('heading', {name: 'Frogejo'})).toBeVisible();
});

test('Set from query', async ({page}) => {
  const response = await page.goto('/user2/repo1/_new/master?value=This\\nis\\\\nFrogejo!', {waitUntil: 'domcontentloaded'});
  expect(response?.status()).toBe(200);

  await validate(page, 'This\nis\\nFrogejo!');
});

test('Search in file', async ({page}) => {
  const response = await page.goto('/user2/repo1/_new/master?value=This\\nis\\nFrogejo!\\nthIs', {waitUntil: 'domcontentloaded'});
  expect(response?.status()).toBe(200);

  const editor = page.locator('.cm-content');
  const searchField = page.locator('.fj-search input[name="search"]');
  const toggleCase = page.locator('label[for="search_case_sensitive"]');
  const toggleRegex = page.locator('label[for="search_regexp"]');
  const toggleByWord = page.locator('label[for="search_by_word"]');
  const nextButton = page.locator('button[aria-label="Next find"]');

  await validate(page, 'This\nis\nFrogejo!\nthIs');

  await editor.click();

  // Open search
  await page.keyboard.press('ControlOrMeta+F', {delay: 5});
  await expect(searchField).toBeFocused();

  const searchResults = editor.locator('.cm-line > .cm-searchMatch');
  await expect(searchResults).toHaveCount(0);

  await searchField.pressSequentially('Is');
  await expect(searchResults).toHaveCount(3);

  await expect(editor.locator('div:nth-child(1)')).not.toHaveClass(/(^|\s)cm-activeLine(\s|$)/);
  await expect(editor.locator('div:nth-child(2)')).not.toHaveClass(/(^|\s)cm-activeLine(\s|$)/);
  await nextButton.click();
  await expect(editor.locator('div:nth-child(1)')).toHaveClass(/(^|\s)cm-activeLine(\s|$)/);
  await expect(editor.locator('div:nth-child(2)')).not.toHaveClass(/(^|\s)cm-activeLine(\s|$)/);
  await nextButton.click();
  await expect(editor.locator('div:nth-child(1)')).not.toHaveClass(/(^|\s)cm-activeLine(\s|$)/);
  await expect(editor.locator('div:nth-child(2)')).toHaveClass(/(^|\s)cm-activeLine(\s|$)/);

  await toggleByWord.click();
  await expect(searchResults).toHaveCount(1);

  await toggleCase.click();
  await expect(searchResults).toHaveCount(0);

  await toggleByWord.click();
  await expect(searchResults).toHaveCount(1);

  await toggleRegex.click();
  await expect(searchResults).toHaveCount(1);

  await toggleCase.click();
  await searchField.clear();
  await expect(searchResults).toHaveCount(0);

  await searchField.pressSequentially('^is$');
  await expect(searchResults).toHaveCount(1);

  await page.locator('#editor-find').click();
  await expect(searchResults).toHaveCount(0);
  await expect(searchField).toHaveCount(0);
});

test('Replace in file', async ({page}) => {
  const response = await page.goto('/user2/repo1/_new/master?value=This\\nis\\nFrogejo!\\nthIs', {waitUntil: 'domcontentloaded'});
  expect(response?.status()).toBe(200);

  const editor = page.locator('.cm-content');
  const searchField = page.locator('.fj-search input[name="search"]');
  const replaceField = page.locator('.fj-search input[name="replace"]');

  await validate(page, 'This\nis\nFrogejo!\nthIs');

  await editor.click();

  // Open search
  await page.locator('#editor-find').click();
  await expect(searchField).toBeFocused();

  await searchField.pressSequentially('Is');
  await replaceField.pressSequentially('Blub');

  await page.getByRole('button', {name: 'Replace all'}).click();

  await validate(page, 'ThBlub\nBlub\nFrogejo!\nthBlub');
});

test('Do not open search if search button not available', async ({page}) => {
  const response = await page.goto('/user2/repo1/settings/hooks/git/pre-receive', {waitUntil: 'domcontentloaded'});
  expect(response?.status()).toBe(200);

  const editor = page.locator('.cm-content');
  const searchField = page.locator('.fj-search input[name="search"]');

  await expect(page.locator('#editor-find')).toHaveCount(0);
  await editor.click();

  await page.keyboard.press('ControlOrMeta+F', {delay: 5});
  await expect(searchField).toHaveCount(0);
});

test.describe('JS off', () => {
  test.use({javaScriptEnabled: false});

  test('Commit button is enabled by default', async ({page}) => {
    const response = await page.goto('/user2/repo1/_new/master', {waitUntil: 'domcontentloaded'});
    expect(response?.status()).toBe(200);

    const commitButton = page.getByRole('button', {name: 'Commit changes'});
    await expect(commitButton).toBeVisible();
    await expect(commitButton).toBeEnabled();
  });
});

test.describe('JS on', () => {
  test('Commit button is disabled by default', async ({page}) => {
    const response = await page.goto('/user2/repo1/_new/master', {waitUntil: 'load'});
    expect(response?.status()).toBe(200);

    const commitButton = page.getByRole('button', {name: 'Commit changes'});
    await expect(commitButton).toBeVisible();
    await expect(commitButton).toBeDisabled();
  });

  test('Commit button still disabled on edit to main form', async ({page}) => {
    const response = await page.goto('/user2/repo1/_new/master', {waitUntil: 'load'});
    expect(response?.status()).toBe(200);

    const commitButton = page.getByRole('button', {name: 'Commit changes'});
    await expect(commitButton).toBeDisabled();
    await page.getByRole('textbox', {name: 'Add "<filename>"'}).fill('test');
    await expect(commitButton).toBeDisabled();
  });

  test('Commit button enabled on edit to file name', async ({page}) => {
    const response = await page.goto('/user2/repo1/_new/master', {waitUntil: 'load'});
    expect(response?.status()).toBe(200);

    const commitButton = page.getByRole('button', {name: 'Commit changes'});
    await expect(commitButton).toBeDisabled();
    await enterFilename(page, 'test');
    await expect(commitButton).toBeEnabled();
  });

  test('Commit button enabled on edit to file content', async ({page}) => {
    const response = await page.goto('/user2/repo1/_new/master', {waitUntil: 'load'});
    expect(response?.status()).toBe(200);

    const commitButton = page.getByRole('button', {name: 'Commit changes'});
    await expect(commitButton).toBeDisabled();
    await page.locator('.cm-content').fill('test');
    await expect(commitButton).toBeEnabled();
  });
});

test.describe('Reload protection', () => {
  test('choose to stay', async ({page}) => {
    await page.goto('/user2/repo1/_new/master', {waitUntil: 'load'});

    let didShowDialog = false;
    page.on('dialog', async (dialog) => {
      expect(dialog.type()).toBe('beforeunload');
      await dialog.dismiss(); // "stay on page"
      didShowDialog = true;
    });

    let didReload = false;
    page.on('domcontentloaded', () => {
      // 'domcontentloaded' happens before 'load', so this fn only happens after a ReLoad!
      didReload = true;
    });

    const codeInput = page.locator('.cm-content');
    await codeInput.press('Control+a');
    await codeInput.pressSequentially('test'); // .fill() doesn't count as user interaction on firefox, required for beforeunload to fire

    await page.evaluate(() => window.location.reload()); // navigation won't actually occur
    await expect(() => expect(didShowDialog).toBe(true)).toPass();
    await expect(page.locator('.cm-line').first()).toHaveText('test'); // text content remains (we didn't reload!)
    expect(didReload).toBe(false); // doing this last, to avoid race conditions
  });

  test('choose to leave', async ({page, browserName}) => {
    await page.goto('/user2/repo1/_new/master', {waitUntil: 'load'});

    let didShowDialog = false;
    page.on('dialog', async (dialog) => {
      expect(dialog.type()).toBe('beforeunload');
      await dialog.accept(); // "I'm sure. Reload!"
      didShowDialog = true;
    });

    const codeInput = page.locator('.cm-content');
    await codeInput.press('Control+a');
    await codeInput.pressSequentially('test'); // .fill() doesn't count as user interaction on firefox, required for beforeunload to fire

    await page.reload({waitUntil: 'domcontentloaded'});
    await expect(() => expect(didShowDialog).toBe(true)).toPass();
    if (browserName === 'firefox') {
      await expect(page.locator('.cm-line').first()).toHaveText('test'); // text is restored! (firefox-only behavior)
    } else {
      await expect(page.locator('.cm-line').first()).toBeEmpty(); // text is gone!
    }
  });

  test('choose to stay, remove all text, then leave without bother', async ({page}) => {
    await page.goto('/user2/repo1/_new/master', {waitUntil: 'load'});

    let didShowDialog = false;
    page.on('dialog', async (dialog) => {
      expect(dialog.type()).toBe('beforeunload');
      await dialog.dismiss(); // "stay on page"
      didShowDialog = true;
    });

    let didReload = false;
    page.on('domcontentloaded', () => {
      didReload = true;
    });

    const codeInput = page.locator('.cm-content');
    await codeInput.press('Control+a');
    await codeInput.pressSequentially('test');

    await page.evaluate(() => window.location.reload()); // navigation won't actually occur
    await expect(() => expect(didShowDialog).toBe(true)).toPass();
    didShowDialog = false;
    await expect(page.locator('.cm-line').first()).toHaveText('test');
    expect(didReload).toBe(false);

    // clear the text field (interactively, so that beforeunload runs)
    for (const _ of 'test') {
      await codeInput.press('Backspace');
    }

    await page.reload({waitUntil: 'domcontentloaded'});
    expect(didShowDialog).toBe(false);
    await expect(() => expect(didReload).toBe(true)).toPass();
    await expect(page.locator('.cm-line').first()).toBeEmpty(); // text is gone! (reload didn't resurrect it)
  });

  test('no edits, leave without bother', async ({page}) => {
    await page.goto('/user2/repo1/_new/master', {waitUntil: 'load'});
    page.on('dialog', () => {
      // wait until timeout :)
    });

    await expect(page.locator('.cm-line').first()).toBeEmpty();

    await page.locator('body').click(); // some interaction...
    await page.reload({waitUntil: 'domcontentloaded'}); // this would freeze if the page is waiting on an "Are you sure?" dialog
    await expect(page.locator('.cm-line').first()).toBeEmpty();
  });
});

// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// @watch start
// web_src/js/features/comp/**
// web_src/js/features/repo-**
// templates/repo/issue/view_content/**
// routers/web/repo/issue_content_history.go
// web_src/js/vendor/jquery.are-you-sure.js
// @watch end

import {expect} from '@playwright/test';
import {test, dynamic_id, login_user} from './utils_e2e.ts';
import {screenshot} from './shared/screenshots.ts';

test.use({user: 'user2'});

for (const run of [
  {title: 'JS off', useJs: false},
  {title: 'JS on', useJs: true},
]) {
  test.describe(`Create issue & comment, delete comment`, () => {
    // playwright/valid-title says: [error] Title must be a string
    test(`${run.title}`, async ({browser}, workerInfo) => {
      test.skip(['Mobile Chrome'].includes(workerInfo.project.name), 'Mobile Chrome has trouble clicking Comment button with JS enabled');

      const issueTitle = dynamic_id();
      const issueContent = dynamic_id();
      const commentContent = dynamic_id();

      const context = await login_user(browser, workerInfo, 'user2', {javaScriptEnabled: run.useJs});
      const page = await context.newPage();

      let response = await page.goto('/user2/repo1/issues/new');
      expect(response?.status()).toBe(200);

      // Create a new issue
      await page.getByPlaceholder('Title').fill(issueTitle);
      await page.getByPlaceholder('Leave a comment').fill(issueContent);
      await page.getByRole('button', {name: 'Create issue'}).click();

      if (run.useJs) {
        await expect(page).toHaveURL(/\/user2\/repo1\/issues\/\d+$/);
      } else {
        // NoJS clients end up on a .../comments JSON file and browsers surround it with some HTML
        const redirectUrl = await JSON.parse(await page.locator('body').textContent())['redirect'];
        response = await page.goto(redirectUrl);
        expect(response?.status()).toBe(200);
      }

      // Leave a comment
      await page.locator('#comment-form').getByPlaceholder('Leave a comment').fill(commentContent);
      await page.locator('#comment-form button.primary').filter({hasText: 'Comment'}).click();

      // NoJS has bad UX: user gets sent to a page with raw JSON response
      if (!run.useJs) {
        const redirectUrl = await JSON.parse(await page.locator('body').textContent())['redirect'];
        // Follow the redirect link in the response to get back to the issue
        response = await page.goto(redirectUrl);
        expect(response?.status()).toBe(200);
      }

      // Validate the page contents that actions above made a difference
      await expect(page.locator('h1')).toContainText(issueTitle);
      await expect(page.locator('.comment').filter({hasText: issueContent})).toHaveCount(1);
      await expect(page.locator('.comment').filter({hasText: commentContent})).toHaveCount(1);

      // Delete the newly created comment
      if (run.useJs) {
        // Not possible w/o JS ATM
        await page.locator('.comment').filter({hasText: commentContent}).getByLabel('Comment menu').click();
        page.on('dialog', (dialog) => dialog.accept());
        await page.locator('.comment').filter({hasText: commentContent}).locator('.dialog-dropdown dialog button[data-url$="/delete"]').click();
        await expect(page.locator('.comment').filter({hasText: commentContent})).toHaveCount(0);
      }
    });
  });
}

test.describe('Reload protection', () => {
  test('choose to stay', async ({page}) => {
    await page.goto('/user2/repo1/issues/1', {waitUntil: 'load'});

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

    const replyInput = page.getByRole('textbox', {name: 'Leave a comment'});
    await expect(replyInput).toBeEmpty();
    await replyInput.pressSequentially('i can\'t believe it\'s not jQuery!'); // .fill() doesn't count as user interaction on firefox, required for beforeunload to fire

    await page.evaluate(() => window.location.reload()); // navigation won't actually occur
    await expect(() => expect(didShowDialog).toBe(true)).toPass();
    await expect(replyInput).toHaveValue('i can\'t believe it\'s not jQuery!'); // text content remains (we didn't reload!)
    expect(didReload).toBe(false); // doing this last, to avoid race conditions
  });

  test('choose to leave', async ({page, browserName}) => {
    await page.goto('/user2/repo1/issues/1', {waitUntil: 'load'});

    let didShowDialog = false;
    page.on('dialog', async (dialog) => {
      expect(dialog.type()).toBe('beforeunload');
      await dialog.accept(); // "I'm sure. Reload!"
      didShowDialog = true;
    });

    const replyInput = page.getByRole('textbox', {name: 'Leave a comment'});
    await expect(replyInput).toBeEmpty();
    await replyInput.pressSequentially('i can\'t believe it\'s not jQuery!');

    await page.reload({waitUntil: 'domcontentloaded'});
    await expect(() => expect(didShowDialog).toBe(true)).toPass();
    if (browserName === 'firefox') {
      await expect(replyInput).toHaveValue('i can\'t believe it\'s not jQuery!'); // text is restored! (firefox-only behavior)
    } else {
      await expect(replyInput).toBeEmpty(); // text is gone!
    }
  });

  test('choose to stay, remove all text, then leave without bother', async ({page}) => {
    await page.goto('/user2/repo1/issues/1', {waitUntil: 'load'});

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

    const replyInput = page.getByRole('textbox', {name: 'Leave a comment'});
    await expect(replyInput).toBeEmpty();
    await replyInput.pressSequentially('i can\'t believe it\'s not jQuery!');

    await page.evaluate(() => window.location.reload()); // navigation won't actually occur
    await expect(() => expect(didShowDialog).toBe(true)).toPass();
    didShowDialog = false;
    await expect(replyInput).toHaveValue('i can\'t believe it\'s not jQuery!'); // text content remains (we didn't reload!)
    expect(didReload).toBe(false);

    // clear the text field (interactively, so that beforeunload runs)
    for (const _ of 'i can\'t believe it\'s not jQuery!') {
      await replyInput.press('Backspace');
    }

    await page.reload({waitUntil: 'domcontentloaded'});
    expect(didShowDialog).toBe(false);
    await expect(() => expect(didReload).toBe(true)).toPass();
    await expect(replyInput).toBeEmpty(); // text gone! (reload didn't resurrect it)
  });

  test('no edits, leave without bother', async ({page}) => {
    await page.goto('/user2/repo1/issues/1', {waitUntil: 'load'});
    page.on('dialog', () => {
      // wait until timeout :)
    });

    const replyInput = page.getByRole('textbox', {name: 'Leave a comment'});
    await expect(replyInput).toBeEmpty();

    await page.locator('body').click(); // some interaction...
    await page.reload({waitUntil: 'domcontentloaded'}); // this would freeze if the page is waiting on an "Are you sure?" dialog
    await expect(replyInput).toBeEmpty();
  });
});

test('Menu accessibility', async ({page}) => {
  await page.goto('/user2/repo1/issues/1');
  await expect(page.getByLabel('user2 reacted eyes. Remove eyes')).toBeVisible();
  await expect(page.getByLabel('reacted laugh. Remove laugh')).toBeVisible();
  await expect(page.locator('#issue-1').getByLabel('Comment menu')).toBeVisible();
  await expect(page.locator('#issue-1').getByRole('heading').getByLabel('Add reaction')).toBeVisible();
  page.getByLabel('reacted laugh. Remove').click();
  await expect(page.getByLabel('user1 reacted laugh. Add laugh')).toBeVisible();
  page.getByLabel('user1 reacted laugh.').click();
  await expect(page.getByLabel('user1, user2 reacted laugh. Remove laugh')).toBeVisible();
});

test.describe('Button text replaced by JS', () => {
  async function testPage(page, path, closeLabel) {
    await page.goto(path);

    const statusButton = page.locator('#status-button');
    const statusButtonIcon = page.locator('#status-button svg');
    const commentField = page.locator('#comment-form').getByPlaceholder('Leave a comment');
    const readyEditor = page.locator('#comment-form .tab[data-tab="markdown-writer-0"]');

    // Reset issue status before running the test
    if (await statusButton.getByText('Reopen').isVisible()) await statusButton.click();

    // Assert that normal Close button text is present
    await readyEditor.waitFor();
    await expect(statusButton.getByText(closeLabel)).toBeVisible();
    await expect(statusButtonIcon).toBeVisible();

    // Type in some text to make button text change
    await readyEditor.waitFor();
    await commentField.fill('Blah blah');
    await expect(statusButton.getByText('Close with comment')).toBeVisible();
    await expect(statusButtonIcon).toBeVisible();

    // Close issue/PR and assert that normal Reopen button text is present
    await statusButton.click();
    await readyEditor.waitFor();
    await expect(statusButton.getByText('Reopen')).toBeVisible();
    await expect(statusButtonIcon).toBeVisible();

    // Type in some text to make button text change
    await readyEditor.waitFor();
    await commentField.fill('Blah blah');
    await expect(statusButton.getByText('Reopen with comment')).toBeVisible();
    await expect(statusButtonIcon).toBeVisible();
  }

  test('Issue', async ({page}) => {
    await expect(async () => {
      await testPage(page, '/user2/repo2/issues/2', 'Close issue');
    }).toPass();
  });

  test('PR', async ({page}) => {
    await expect(async () => {
      await testPage(page, '/user2/repo1/pulls/5', 'Close pull request');
    }).toPass();
  });
});

test('Hyperlink paste behaviour', async ({page, isMobile}) => {
  test.skip(isMobile, 'Mobile clients seem to have very weird behaviour with this test, which I cannot confirm with real usage');
  await page.goto('/user2/repo1/issues/new');
  await page.locator('textarea').click();
  // same URL
  await page.locator('textarea').fill('https://codeberg.org/forgejo/forgejo#some-anchor');
  await page.locator('textarea').press('Shift+Home');
  await page.locator('textarea').press('ControlOrMeta+c');
  await page.locator('textarea').press('ControlOrMeta+v');
  await expect(page.locator('textarea')).toHaveValue('https://codeberg.org/forgejo/forgejo#some-anchor');
  // other text
  await page.locator('textarea').fill('Some other text');
  await page.locator('textarea').press('ControlOrMeta+a');
  await page.locator('textarea').press('ControlOrMeta+v');
  await expect(page.locator('textarea')).toHaveValue('[Some other text](https://codeberg.org/forgejo/forgejo#some-anchor)');
  // subset of URL
  await page.locator('textarea').fill('https://codeberg.org/forgejo/forgejo#some');
  await page.locator('textarea').press('ControlOrMeta+a');
  await page.locator('textarea').press('ControlOrMeta+v');
  await expect(page.locator('textarea')).toHaveValue('https://codeberg.org/forgejo/forgejo#some-anchor');
  // superset of URL
  await page.locator('textarea').fill('https://codeberg.org/forgejo/forgejo#some-anchor-on-the-page');
  await page.locator('textarea').press('ControlOrMeta+a');
  await page.locator('textarea').press('ControlOrMeta+v');
  await expect(page.locator('textarea')).toHaveValue('https://codeberg.org/forgejo/forgejo#some-anchor');
  // completely separate URL
  await page.locator('textarea').fill('http://example.com');
  await page.locator('textarea').press('ControlOrMeta+a');
  await page.locator('textarea').press('ControlOrMeta+v');
  await expect(page.locator('textarea')).toHaveValue('https://codeberg.org/forgejo/forgejo#some-anchor');
  await page.locator('textarea').fill('');
});

test('Always focus edit tab first on edit', async ({page}) => {
  const response = await page.goto('/user2/repo1/issues/1');
  expect(response?.status()).toBe(200);

  const opener = page.locator('#issue-1 .comment-container .dialog-dropdown .opener');
  const editButton = page.locator('#issue-1 .comment-container .dialog-dropdown dialog .edit-content');

  // Switch to preview tab and save
  await opener.click();
  await editButton.click();
  await page.locator('#issue-1 .comment-container [data-tab-for=markdown-previewer]').click();
  await page.click('#issue-1 .comment-container .save');

  await page.waitForLoadState();

  // Edit again and assert that edit tab should be active (and not preview tab)
  await opener.click();
  await editButton.click();
  const editTab = page.locator('#issue-1 .comment-container [data-tab-for=markdown-writer]');
  const previewTab = page.locator('#issue-1 .comment-container [data-tab-for=markdown-previewer]');

  await expect(editTab).toHaveClass(/active/);
  await expect(previewTab).not.toHaveClass(/active/);
  await screenshot(page, page.locator('.issue-content-left'));
});

test('Reset content of comment edit field on cancel', async ({page}) => {
  const response = await page.goto('/user2/repo1/issues/1');
  expect(response?.status()).toBe(200);

  const opener = page.locator('#issue-1 .comment-container .dialog-dropdown .opener');
  const editButton = page.locator('#issue-1 .comment-container .dialog-dropdown dialog .edit-content');
  const editorTextarea = page.locator('[id="_combo_markdown_editor_1"]');

  // Change the content of the edit field
  await opener.click();
  await editButton.click();
  await expect(editorTextarea).toHaveValue('content for the first issue');
  await editorTextarea.fill('some random string');
  await expect(editorTextarea).toHaveValue('some random string');
  await page.click('#issue-1 .comment-container .edit .cancel');

  // Edit again and assert that the edit field should be reset to the initial content
  await opener.click();
  await editButton.click();
  await expect(editorTextarea).toHaveValue('content for the first issue');
  await screenshot(page, page.locator('.issue-content-left'));
});

test('Quote reply', async ({page}, workerInfo) => {
  test.skip(workerInfo.project.name !== 'firefox', 'Uses Firefox specific selection quirks');
  const response = await page.goto('/user2/repo1/issues/1');
  expect(response?.status()).toBe(200);

  const opener = page.locator('#issuecomment-1001 .comment-container .dialog-dropdown .opener');
  const editorTextarea = page.locator('textarea.markdown-text-editor');

  // Full quote.
  await opener.click();
  await page.click('#issuecomment-1001 .quote-reply');

  await expect(editorTextarea).toHaveValue('@user2 wrote in http://localhost:3003/user2/repo1/issues/1#issuecomment-1001:\n\n' +
                                           '> ## [](#lorem-ipsum)Lorem Ipsum\n' +
                                           '> \n' +
                                           '> I would like to say that **I am not appealed** that it took _so long_ for this `feature` to be [created](https://example.com) \\(e^{\\pi i} + 1 = 0\\)\n' +
                                           '> \n' +
                                           '> \\[e^{\\pi i} + 1 = 0\\]\n' +
                                           '> \n' +
                                           '> #1\n' +
                                           '> \n' +
                                           '> ```js\n' +
                                           "> console.log('evil')\n" +
                                           "> alert('evil')\n" +
                                           '> ```\n' +
                                           '> \n' +
                                           '> :+1: :100: [![hi there](/attachments/3f4f4016-877b-46b3-b79f-ad24519a9cf2)](/user2/repo1/attachments/3f4f4016-877b-46b3-b79f-ad24519a9cf2)\n' +
                                           '> <img alt="something something" width="500" height="500" src="/attachments/3f4f4016-877b-46b3-b79f-ad24519a9cf2">\n\n');

  await editorTextarea.fill('');

  // Partial quote.
  await opener.click();

  await page.evaluate(() => {
    const range = new Range();
    range.setStart(document.querySelector('#issuecomment-1001-content #user-content-lorem-ipsum').childNodes[1], 6);
    range.setEnd(document.querySelector('#issuecomment-1001-content p').childNodes[1].childNodes[0], 7);

    const selection = window.getSelection();

    // Add range to window selection
    selection.addRange(range);
  });

  await page.click('#issuecomment-1001 .quote-reply');

  await expect(editorTextarea).toHaveValue('@user2 wrote in http://localhost:3003/user2/repo1/issues/1#issuecomment-1001:\n\n' +
                                           '> ## Ipsum\n' +
                                           '> \n' +
                                           '> I would like to say that **I am no**\n\n');

  await editorTextarea.fill('');

  // Another partial quote.
  await opener.click();

  await page.evaluate(() => {
    const range = new Range();
    range.setStart(document.querySelector('#issuecomment-1001-content p').childNodes[1].childNodes[0], 7);
    range.setEnd(document.querySelector('#issuecomment-1001-content p').childNodes[7].childNodes[0], 3);

    const selection = window.getSelection();

    // Add range to window selection
    selection.addRange(range);
  });

  await page.click('#issuecomment-1001 .quote-reply');

  await expect(editorTextarea).toHaveValue('@user2 wrote in http://localhost:3003/user2/repo1/issues/1#issuecomment-1001:\n\n' +
                                           '> **t appealed** that it took _so long_ for this `feature` to be [cre](https://example.com)\n\n');

  await editorTextarea.fill('');
});

test('Pull quote reply', async ({page}, workerInfo) => {
  test.skip(workerInfo.project.name !== 'firefox', 'Uses Firefox specific selection quirks');
  const response = await page.goto('/user2/commitsonpr/pulls/1/files');
  expect(response?.status()).toBe(200);

  const editorTextarea = page.locator('form.comment-form textarea.markdown-text-editor');

  // Full quote with no reply handler being open.
  await page.click('.comment-code-cloud .dialog-dropdown .opener');
  await page.click('.comment-code-cloud .quote-reply');

  await expect(editorTextarea).toHaveValue('@user2 wrote in http://localhost:3003/user2/commitsonpr/pulls/1/files#issuecomment-1002:\n\n' +
                                           '> ## [](#lorem-ipsum)Lorem Ipsum\n' +
                                           '> \n' +
                                           '> I would like to say that **I am not appealed** that it took _so long_ for this `feature` to be [created](https://example.com) \\(e^{\\pi i} + 1 = 0\\)\n' +
                                           '> \n' +
                                           '> \\[e^{\\pi i} + 1 = 0\\]\n' +
                                           '> \n' +
                                           '> #1\n' +
                                           '> \n' +
                                           '> ```js\n' +
                                           "> console.log('evil')\n" +
                                           "> alert('evil')\n" +
                                           '> ```\n' +
                                           '> \n' +
                                           '> :+1: :100: [![hi there](/attachments/3f4f4016-877b-46b3-b79f-ad24519a9cf2)](/user2/commitsonpr/attachments/3f4f4016-877b-46b3-b79f-ad24519a9cf2)\n' +
                                           '> <img alt="something something" width="500" height="500" src="/attachments/3f4f4016-877b-46b3-b79f-ad24519a9cf2">\n\n');

  await editorTextarea.fill('');
});

test('Emoji suggestions', async ({page}) => {
  const response = await page.goto('/user2/repo1/issues/1');
  expect(response?.status()).toBe(200);

  const textarea = page.locator('#comment-form textarea[name=content]');

  await textarea.focus();
  await textarea.pressSequentially(':');

  const suggestionList = page.locator('#comment-form .suggestions');
  await expect(suggestionList).toBeVisible();

  const expectedSuggestions = [
    {emoji: '👍', name: '+1'},
    {emoji: '👎', name: '-1'},
    {emoji: '💯', name: '100'},
    {emoji: '🔢', name: '1234'},
    {emoji: '🥇', name: '1st_place_medal'},
    {emoji: '🥈', name: '2nd_place_medal'},
  ];

  for (const {emoji, name} of expectedSuggestions) {
    const item = suggestionList.locator(`[id="combobox-emoji-${name}"]`);
    await expect(item).toContainText(`${emoji} ${name}`);
  }

  await textarea.pressSequentially('forge');
  await expect(suggestionList).toBeVisible();

  const item = suggestionList.locator(`li:has-text("forgejo")`);
  await expect(item.locator('img')).toHaveAttribute('src', '/assets/img/emoji/forgejo.png');
});

test.describe('Comment history', () => {
  let issueURL = '';

  test('Deleted items in comment history menu', async ({page}) => {
    const response = await page.goto('/user2/repo1/issues/new');
    expect(response?.status()).toBe(200);

    // Create a new issue.
    await page.getByPlaceholder('Title').fill('Just a title');
    await page.getByPlaceholder('Leave a comment').fill('Hi, have you considered using a rotating fish as logo?');
    await page.getByRole('button', {name: 'Create issue'}).click();
    await expect(page).toHaveURL(/\/user2\/repo1\/issues\/\d+$/);
    issueURL = page.url();

    page.on('dialog', (dialog) => dialog.accept());

    // Make a change.
    const editorTextarea = page.locator('[id="_combo_markdown_editor_1"]');
    await page.click('.comment-container .dialog-dropdown .opener');
    await page.click('.comment-container .dialog-dropdown dialog .edit-content');
    await editorTextarea.fill(dynamic_id());
    await page.click('.comment-container .edit .save');
    // Programmatically requesting page.reload() is too quick and can result in edit save request being aborted, especially on Firefox. Wait for JS to update attribute with contentVersion value from server reply
    await expect(page.locator('.edit-content-zone:has([id="_combo_markdown_editor_1"])')).toHaveAttribute('data-content-version', '1');

    // Reload the page so the edited bit is rendered.
    await page.reload();

    await page.getByText('• edited').click();
    await page.click('.content-history-menu .item:nth-child(1)');
    await page.getByText('Options').click();
    await page.getByText('Delete from history').click();

    await page.getByText('• edited').click();
    await expect(page.locator(".content-history-menu .item s span[data-history-is-deleted='1']")).toBeVisible();
  });

  test('Animation spinner', async ({page}) => {
    test.skip(issueURL === '', 'previous test failed');

    const response = await page.goto(issueURL);
    expect(response?.status()).toBe(200);

    // Intercept request to get content history list.
    let called = false;
    page.on('request', async (request) => {
      if (!request.url().includes('/content-history/list')) {
        return;
      }
      called = true;
      // Assert the dropdown has a animation spinner.
      await expect(page.getByText('• edited')).toHaveClass(/is-loading/);
    });

    // Open the menu.
    await page.getByText('• edited').click();
    // Wait until the menu is visible.
    await expect(page.locator('.content-history-menu .item:nth-child(1)')).toBeVisible();
    // Expect that there was a request by fomantic.
    expect(called).toBeTruthy();
    // Expect that there is no longer a animation spinner.
    await expect(page.getByText('• edited')).not.toHaveClass(/is-loading/);

    // Expect that there is no animation spinner after clicking inside the dropdown.
    await page.click('.content-history-menu .item:nth-child(2)');
    await expect(page.getByText('• edited')).not.toHaveClass(/is-loading/);
    await page.click('.content-history-detail-dialog .close');

    // Open the menu.
    await page.getByText('• edited').click();
    // Wait until the menu is visible.
    await expect(page.locator('.content-history-menu .item:nth-child(1)')).toBeVisible();
    // Close the menu.
    await page.getByText('• edited').click();
    // Expect that there is no animation spinner.
    await expect(page.getByText('• edited')).not.toHaveClass(/is-loading/);
  });
});

test.describe('Markdown rendered on preview', () => {
  const previewButtonSelector = '#comment-form [data-tab-for=markdown-previewer]';
  const linkSelector = '#comment-form [data-tab-panel=markdown-previewer] a';

  async function getColorByPropertyName(page, propertyName) {
    return await page.locator('html').evaluate((element, pname) => {
      return window.getComputedStyle(element).getPropertyValue(pname).trim();
    }, propertyName);
  }

  function hexToRGBA(hex) {
    const hexArr = hex.slice(1).match(/.{2}/g);
    const [r, g, b, a] = hexArr.map((h) => parseInt(h, 16));
    return `rgba(${r}, ${g}, ${b}, ${(a / 255).toFixed(3)})`;
  }

  test.beforeEach(async ({page}) => {
    await page.goto('/user2/repo1/issues/1');
  });

  test('Mention the current user', async ({page}) => {
    // write comment with mention
    await page.getByPlaceholder('Leave a comment').fill('@user2 a link in a pill');

    // click on the preview tab
    await page.locator(previewButtonSelector).click();

    // check content
    const colorHex = await getColorByPropertyName(page, '--color-primary-alpha-30');
    const colorRGBA = hexToRGBA(colorHex);
    const link = page.locator(linkSelector);
    await expect(link).toHaveCSS('background-color', colorRGBA);
  });

  test('Mention a private user', async ({page}) => {
    // write comment with mention
    await page.getByPlaceholder('Leave a comment').fill('@limited_org a plain link');

    // click on the preview tab
    await page.locator(previewButtonSelector).click();

    // check content
    const link = page.locator(linkSelector);
    await expect(link).toHaveText('@limited_org');
  });
});

test('Reference in a new issue', async ({page}) => {
  await page.goto('/user2/repo2/issues/1');

  const dropdownWrap = page.locator('.first.comment .comment-header .dialog-dropdown');
  const dropdownContent = dropdownWrap.locator('dialog');
  const modal = page.locator('#reference-issue-modal');

  // Open comment actions
  await dropdownWrap.locator('.opener').click();
  await expect(dropdownContent).toBeVisible();

  // Clicking "Reference in a new issue" opens dialog and closes dropdown
  await dropdownContent.locator('.reference-issue').click();
  await expect(modal).toBeVisible();
  await expect(dropdownContent).toBeHidden();

  // Pressing Escape closes the modal
  await page.keyboard.press('Escape');
  await expect(modal).toBeHidden();
});

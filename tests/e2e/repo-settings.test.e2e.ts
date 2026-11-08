// @watch start
// templates/webhook/shared-settings.tmpl
// templates/repo/settings/**
// web_src/css/{form,repo}.css
// web_src/css/modules/grid.css
// web_src/js/features/comp/WebHookEditor.js
// web_src/js/features/repo-legacy.js
// web_src/js/features/repo-settings.js
// @watch end

import {expect} from '@playwright/test';
import {test, dynamic_id} from './utils_e2e.ts';
import {screenshot} from './shared/screenshots.ts';
import {validate_form} from './shared/forms.ts';

test.use({user: 'user2'});

test('repo webhook settings', async ({page}) => {
  const response = await page.goto('/user2/repo1/settings/hooks/forgejo/new');
  expect(response?.status()).toBe(200);

  await page.locator('input[name="events"][value="choose_events"]').click();
  await expect(page.locator('.hide-unless-checked')).toBeVisible();

  // check accessibility including the custom events (now visible) part
  await validate_form({page}, 'fieldset');
  await screenshot(page);

  await page.locator('input[name="events"][value="push_only"]').click();
  await expect(page.locator('.hide-unless-checked')).toBeHidden();
  await page.locator('input[name="events"][value="send_everything"]').click();
  await expect(page.locator('.hide-unless-checked')).toBeHidden();
  await screenshot(page);
});

test.describe('repo branch protection settings', () => {
  test.afterEach(async ({page}) => {
    // delete the rule for the next test
    await page.goto('/user2/repo1/settings/branches/');
    await page.waitForLoadState('domcontentloaded');
    const deleteButton = page.locator('.delete-button').first();
    test.skip(await deleteButton.isHidden(), 'Nothing to delete at this time');
    await deleteButton.click();
    await page.locator('#delete-protected-branch .actions .ok').click();
    // Here page.waitForLoadState('domcontentloaded') does not work reliably.
    // Instead, wait for the delete button to disappear.
    await expect(deleteButton).toHaveCount(0);
  });

  test('form', async ({page}) => {
    const response = await page.goto('/user2/repo1/settings/branches/edit');
    expect(response?.status()).toBe(200);

    await validate_form({page}, 'fieldset');

    // verify header is new
    await expect(page.locator('h4')).toContainText('new');
    await page.locator('input[name="rule_name"]').fill('testrule');
    await screenshot(page);
    await page.locator('button:text("Save rule")').click();
    // verify header is in edit mode
    await page.waitForLoadState('domcontentloaded');
    await screenshot(page);

    // find the edit button and click it
    const editButton = page.locator('a[href="/user2/repo1/settings/branches/edit?rule_name=testrule"]');
    await editButton.click();

    await page.waitForLoadState();
    await expect(page.locator('.repo-setting-content .header')).toContainText('Protection rules for branch', {ignoreCase: true, useInnerText: true});
    await screenshot(page);
  });
});

test.describe('repo actions secrets settings', () => {
  const secretName = dynamic_id().replaceAll('-', '_').toUpperCase();

  test('create secret', async ({page}) => {
    const response = await page.goto('/user2/repo1/settings/actions/secrets');
    expect(response?.status()).toBe(200);

    await page.getByText('Add secret').click();
    const modal = page.locator('#add-secret-modal');
    await expect(modal).toBeVisible();

    await modal.getByLabel('Name').fill(secretName);
    await modal.getByLabel('Value').fill('All Right Then, Keep Your Secrets');
    await modal.getByText('Confirm').click();

    await expect(page.getByText(secretName, {exact: true})).toBeVisible();
  });

  test('delete secret', async ({page}) => {
    const response = await page.goto('/user2/repo1/settings/actions/secrets');
    expect(response?.status()).toBe(200);

    const deleteButton = page.locator('.flex-item').filter({hasText: secretName}).getByLabel('Remove secret');
    await deleteButton.click();
    const modal = page.locator('#delete-secret');
    await expect(modal).toBeVisible();

    await modal.getByText('Confirm').click();
    await expect(page.getByText('There are no secrets yet.')).toBeVisible();
  });
});

test.describe('repo actions variables settings', () => {
  const variableName = dynamic_id().replaceAll('-', '_').toUpperCase();

  test('create variable', async ({page}) => {
    const response = await page.goto('/user2/repo1/settings/actions/variables');
    expect(response?.status()).toBe(200);

    await page.getByText('Add variable').click();
    const modal = page.locator('#edit-variable-modal');
    await expect(modal).toBeVisible();

    await modal.getByLabel('Name').fill(variableName);
    await modal.getByLabel('Value').fill("Will Frogejo cease to exist if there's a Forgejo trademark?");
    await modal.getByText('Confirm').click();

    await expect(page.getByText(variableName, {exact: true})).toBeVisible();
  });

  test('delete variable', async ({page}) => {
    const response = await page.goto('/user2/repo1/settings/actions/variables');
    expect(response?.status()).toBe(200);

    const deleteButton = page.locator('.flex-item').filter({hasText: variableName}).getByLabel('Remove variable');
    await deleteButton.click();
    const modal = page.locator('#delete-variable');
    await expect(modal).toBeVisible();

    await modal.getByText('Confirm').click();
    await expect(page.getByText('There are no variables yet.')).toBeVisible();
  });
});

test.describe('repo lfs settings', () => {
  test('pointer settings', async ({page}) => {
    const response = await page.goto('/user2/lfs/settings/lfs/pointers');
    expect(response?.status()).toBe(200);
    await screenshot(page);

    // Expect that the checkbox of a not-in-repo Git LFS file is disabled
    const disabledCheckbox = page.locator('input[aria-label="9d178b5f15"]');
    await expect(disabledCheckbox).toBeDisabled();
    const togglableCheckbox = page.locator('input[aria-label="7b6b2c88db"]');

    // We haven't selected anything yet, so this should be disabled.
    const deleteSelectedFiles = page.locator('#lfs-files-delete-button');
    await expect(deleteSelectedFiles).toBeDisabled();

    // Check a checkbox
    await togglableCheckbox.check();
    await expect(togglableCheckbox).toBeChecked({checked: true});
    await screenshot(page);

    // Now check the modal behavior
    await expect(deleteSelectedFiles).toBeEnabled();
    await deleteSelectedFiles.click();
    await expect(page.locator('dialog#lfs-files-delete.modal')).toBeVisible();
    await screenshot(page);

    await page.locator('dialog#lfs-files-delete.modal article > div.actions > button.cancel').click();
    await expect(page.locator('dialog#lfs-files-delete.modal')).toBeHidden();
    await screenshot(page);
  });

  test('pointer settings - (un)select all', async ({page}) => {
    const response = await page.goto('/user2/lfs/settings/lfs/pointers');
    expect(response?.status()).toBe(200);
    await screenshot(page);

    const checkbox1 = page.locator('input[aria-label="9d172e5c64"]');
    const checkbox2 = page.locator('input[aria-label="7b6b2c88db"]');
    const checkbox3 = page.locator('input[aria-label="0b8d8b5f15"]');
    const checkbox4 = page.locator('input[aria-label="9d178b5f15"]');
    const checkbox5 = page.locator('input[aria-label="2eccdb4382"]');

    // Default state
    await expect(checkbox1).toBeChecked({checked: false});
    await expect(checkbox2).toBeChecked({checked: false});
    await expect(checkbox3).toBeChecked({checked: false});
    await expect(checkbox4).toBeChecked({checked: false});
    await expect(checkbox5).toBeChecked({checked: false});

    // Let's "select all" (out of those that are in-repo)
    const selectAllCheckbox = page.locator('input.select-all');
    await selectAllCheckbox.click();

    // Everything that we could've selected is now selected.
    await expect(checkbox1).toBeChecked({checked: true});
    await expect(checkbox2).toBeChecked({checked: true});
    await expect(checkbox3).toBeChecked({checked: true});
    await expect(checkbox4).toBeChecked({checked: false});
    await expect(checkbox5).toBeChecked({checked: true});

    await screenshot(page);

    // Let's unselect everything for good measure, and see if we're back where
    // we started.
    await selectAllCheckbox.click();

    await expect(checkbox1).toBeChecked({checked: false});
    await expect(checkbox2).toBeChecked({checked: false});
    await expect(checkbox3).toBeChecked({checked: false});
    await expect(checkbox4).toBeChecked({checked: false});
    await expect(checkbox5).toBeChecked({checked: false});

    await screenshot(page);
  });
});

test.describe('repo collaboration settings', () => {
  test('add collaborator', async ({page}) => {
    const response = await page.goto('/user2/repo1/settings/collaboration');
    expect(response?.status()).toBe(200);

    await page.getByPlaceholder('Search users…').fill('user5');
    await page.getByRole('button', {name: 'Add collaborator'}).click();

    await expect(page.getByRole('link', {name: 'user5 (User Five)'})).toBeVisible();
  });

  test('change access mode', async ({page}) => {
    const response = await page.goto('/user2/repo1/settings/collaboration');
    expect(response?.status()).toBe(200);

    const opener = page.locator('.access-mode .opener');
    await expect(opener).toHaveText('Write');

    // Check that opener and rmButton are same height
    const rmButton = page.getByRole('button', {name: 'Remove'});
    expect((await opener.boundingBox()).height).toBe((await rmButton.boundingBox()).height);

    // Open dropdown and change access mode to Read
    await opener.click();
    await page.getByRole('button', {name: 'Read'}).click();

    await expect(opener).toHaveText('Read');
  });

  test('remove collaborator', async ({page}) => {
    const response = await page.goto('/user2/repo1/settings/collaboration');
    expect(response?.status()).toBe(200);

    await page.getByRole('button', {name: 'Remove'}).click();
    const modal = page.locator('#delete-collaborator');
    await expect(modal).toBeVisible();
    await modal.getByRole('button', {name: 'Yes'}).click();

    await expect(page.getByText('There are no collaborators yet.')).toBeVisible();
  });
});

// @watch start
// modules/actions/**
// routers/web/shared/actions/**
// routers/web/web.go
// services/forms/runner.go
// templates/admin/runners/**
// templates/org/settings/runner*.tmpl
// templates/repo/settings/runner*.tmpl
// templates/shared/actions/runner*.tmpl
// templates/user/settings/runner*.tmpl
// web_src/css/actions.css
// @watch end

import {test} from './utils_e2e.ts';
import {expect} from '@playwright/test';

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const tokenPattern = /^[0-9a-f]{40}$/;

test.describe('Runners of user2', () => {
  test.use({user: 'user2'});

  test('usable runners are visible', async ({page}) => {
    await page.goto('/user/settings/actions/runners');

    await expect(page.getByRole('heading', {name: 'Manage runners'})).toBeVisible();

    const runnerContainer = page.locator('.runner-container');
    const rows = runnerContainer.getByRole('row');

    // We cannot assert the length of the table because it's influenced by global fixtures. It also changes depending on
    // the ordering of tests.
    await expect(rows.nth(0)).toHaveAccessibleName('Name Labels Type Status Edit Delete');
    await expect(page.locator('tbody tr:has-text("3a20ad8d-d5d6-4b7b-ba55-841ac8264c17")')).toMatchAriaSnapshot(`
      - cell "runner-2 3a20ad8d-d5d6-4b7b-ba55-841ac8264c17":
        - link "runner-2":
          - /url: /user/settings/actions/runners/719932
      - cell "docker"
      - cell "Individual"
      - cell "Offline"
      - cell "Edit runner-2"
      - cell "Delete runner-2"
    `);
    await expect(page.locator('tbody tr:has-text("1ef59b64-93b7-4ad4-ade4-21ca13db49c0")')).toMatchAriaSnapshot(`
      - cell "runner-4 1ef59b64-93b7-4ad4-ade4-21ca13db49c0":
        - link "runner-4":
          - /url: /user/settings/actions/runners/719934
      - cell "docker"
      - cell "Global"
      - cell "Offline"
      - cell
      - cell
    `);

    await page.getByRole('link', {name: 'runner-2', exact: true}).click();
    await expect(page).toHaveTitle(/^Runner runner-2 .*/);

    await page.goto('/user/settings/actions/runners');

    await page.getByRole('link', {name: 'runner-4', exact: true}).click();
    await expect(page).toHaveTitle(/^Runner runner-4 .*/);
  });

  test('runner details with tasks of repositories owned by user', async ({page}) => {
    await page.goto('/user/settings/actions/runners');

    await page.getByRole('link', {name: 'runner-4', exact: true}).click();
    await expect(page).toHaveTitle(/^Runner runner-4 .*/);

    await expect(page.getByRole('heading', {name: 'Runner runner-4'})).toBeVisible();

    await expect(page.getByLabel('Properties of runner-4')).toMatchAriaSnapshot(`
      - term: UUID
      - definition: ${uuidPattern}
      - term: Type
      - definition: Global
      - term: Labels
      - definition: docker
      - term: Last online time
      - definition: Never
      - term: Status
      - definition: Offline
      - term: Ephemeral
      - definition: "no"
      - term: Version
      - definition: 12.2.0
      - term: Description
      - definition: A runner for everyone
    `);

    await expect(page.getByRole('heading', {name: 'Recent tasks of this user on this runner'})).toBeVisible();

    const rows = page.getByRole('row');

    // Only tasks from repositories owned by user2 should appear.
    await expect(rows).toHaveCount(2);
    await expect(rows.nth(0)).toHaveAccessibleName('Run Status Repository Commit Done at');
    await expect(rows.nth(1)).toHaveAccessibleName('88932 Waiting 49f55ab99b -');
  });

  test('Create new runner', async ({page}) => {
    await page.goto('/user/settings/actions/runners');

    const area = page.locator('.page-content');
    await expect(area.getByRole('heading', {name: 'Manage runners'})).toBeVisible();

    // Go to runner creation page
    await area.getByRole('link', {name: 'Create new runner'}).click();
    await expect(page).toHaveTitle(/^New runner .*/);

    // Submit an invalid form to test validation
    await area.getByRole('button', {name: 'Create'}).click();
    await expect(area.getByRole('paragraph')).toHaveText('Name cannot be empty.');

    // Submit a valid form to create a runner
    await area.getByRole('textbox', {name: 'Name *'}).fill('runner-991301');
    await area.getByRole('textbox', {name: 'Description'}).fill('Description of runner-991301');
    await area.getByRole('button', {name: 'Create'}).click();

    // Verify set up instructions.
    await expect(page).toHaveTitle(/^Set up runner runner-991301 .*/);
    await expect(area.getByRole('heading', {name: 'Set up runner runner-991301'})).toBeVisible();

    let runnerUUID;
    await expect(async () => {
      await area.getByRole('button', {name: 'Copy runner UUID'}).click();
      runnerUUID = await area.evaluate(() => navigator.clipboard.readText());
      expect(runnerUUID).toMatch(uuidPattern);
    }).toPass();

    let runnerToken;
    await expect(async () => {
      await area.getByRole('button', {name: 'Copy runner token'}).click();
      runnerToken = await area.evaluate(() => navigator.clipboard.readText());
      expect(runnerToken).toMatch(tokenPattern);
    }).toPass();

    await expect(area.getByRole('term')).toHaveText(['UUID', 'Token']);
    await expect(area.getByRole('definition')).toContainText([runnerUUID, runnerToken]);

    await expect(area.getByRole('heading', {name: 'Using the runner configuration file'})).toBeVisible();
    await expect(area.getByLabel('Snippet to insert into the runner configuration')).toContainText(`uuid: ${runnerUUID}`);
    await expect(area.getByLabel('Snippet to insert into the runner configuration')).toContainText(`token: ${runnerToken}`);

    await expect(area.getByRole('heading', {name: 'Using program options'})).toBeVisible();
    await expect(area.getByLabel('How to invoke forgejo-runner')).toContainText(`--uuid ${runnerUUID}`);
    await expect(area.getByLabel('How to invoke forgejo-runner')).toContainText(`echo -n "${runnerToken}"`);

    // Go back to list of runners.
    await area.getByRole('link', {name: 'List of runners', exact: true}).click();

    await expect(area.locator(`tbody tr:has-text("${runnerUUID}")`)).toMatchAriaSnapshot(`
      - cell "runner-991301 ${runnerUUID}":
        - link "runner-991301":
          - /url: /user/settings/actions/runners/\\d+/
      - cell ""
      - cell "Individual"
      - cell "Offline"
      - cell "Edit runner-991301"
      - cell "Delete runner-991301"
    `);
  });

  test('edit runner without changing its token', async ({page}) => {
    await page.goto('/user/settings/actions/runners');

    const area = page.locator('.page-content');

    // We have to create a new runner because changes to fixtures would affect the remainder of the tests in this file.
    await area.getByRole('link', {name: 'Create new runner'}).click();
    await area.getByRole('textbox', {name: 'Name *'}).fill('runner-46635');
    await area.getByRole('textbox', {name: 'Description'}).fill('Description of runner-46635');
    await area.getByRole('button', {name: 'Create'}).click();

    // Go back to list of runners.
    await area.getByRole('link', {name: 'Runners', exact: true}).click();

    // Edit the runner that was just created.
    await area.getByRole('link', {name: 'Edit runner-46635'}).click();

    await expect(page).toHaveTitle(/^Edit runner runner-46635 .*/);
    await expect(area.getByRole('heading', {name: 'Edit runner runner-46635'})).toBeVisible();

    // Make the form invalid to test validation.
    await area.getByRole('textbox', {name: 'Name *'}).clear();
    await area.getByRole('button', {name: 'Save'}).click();

    await expect(page.locator('#flash-message')).toHaveText('Name cannot be empty.');
    await expect(area.getByRole('textbox', {name: 'Name *'})).toBeEmpty();
    await expect(area.getByRole('textbox', {name: 'Description'})).toHaveValue('Description of runner-46635');

    // Submit a valid form.
    await area.getByRole('textbox', {name: 'Name *'}).fill('runner-46636');
    await area.getByRole('textbox', {name: 'Description'}).fill('Description of runner-46636');

    await area.getByRole('button', {name: 'Save'}).click();

    // Verify that the runner's properties were updated properly.
    await expect(page).toHaveTitle(/^Runner runner-46636 .*/);
    await expect(page.locator('#flash-message')).toHaveText('Runner edited successfully');
    await expect(area.getByRole('heading', {name: 'Runner runner-46636'})).toBeVisible();

    await expect(area.getByLabel('Properties of runner-46636')).toMatchAriaSnapshot(`
      - term: UUID
      - definition: ${uuidPattern}
      - term: Type
      - definition: Individual
      - term: Labels
      - definition
      - term: Last online time
      - definition: Never
      - term: Status
      - definition: Offline
      - term: Ephemeral
      - definition: "no"
      - term: Version
      - definition: —
      - term: Description
      - definition: Description of runner-46636
    `);
  });

  test('Regenerate runner token', async ({page}) => {
    await page.goto('/user/settings/actions/runners');

    await page.getByRole('link', {name: 'Edit runner-2', exact: true}).click();

    await expect(page).toHaveTitle(/^Edit runner runner-2 .*/);
    await expect(page.getByRole('heading', {name: 'Edit runner runner-2'})).toBeVisible();

    await page.getByRole('checkbox', {name: 'Regenerate token'}).check();
    await page.getByRole('button', {name: 'Save'}).click();

    // Verify set up instructions.
    await expect(page).toHaveTitle(/^Set up runner runner-2 .*/);
    await expect(page.getByRole('heading', {name: 'Set up runner runner-2'})).toBeVisible();

    let runnerUUID;
    await expect(async () => {
      await page.getByRole('button', {name: 'Copy runner UUID'}).click();
      runnerUUID = await page.evaluate(() => navigator.clipboard.readText());
      expect(runnerUUID).toEqual('3a20ad8d-d5d6-4b7b-ba55-841ac8264c17');
    }).toPass();

    let runnerToken;
    await expect(async () => {
      await page.getByRole('button', {name: 'Copy runner token'}).click();
      runnerToken = await page.evaluate(() => navigator.clipboard.readText());
      expect(runnerToken).not.toEqual('9730f9d2c6c731f07582788d1a1fe72a6b999a17');
      expect(runnerToken).toMatch(tokenPattern);
    }).toPass();

    await expect(page.getByRole('term')).toHaveText(['UUID', 'Token']);
    await expect(page.getByRole('definition')).toContainText([runnerUUID, runnerToken]);

    await expect(page.getByRole('heading', {name: 'Using the runner configuration file'})).toBeVisible();
    await expect(page.getByLabel('Snippet to insert into the runner configuration')).toContainText(`uuid: ${runnerUUID}`);
    await expect(page.getByLabel('Snippet to insert into the runner configuration')).toContainText(`token: ${runnerToken}`);

    await expect(page.getByRole('heading', {name: 'Using program options'})).toBeVisible();
    await expect(page.getByLabel('How to invoke forgejo-runner')).toContainText(`--uuid ${runnerUUID}`);
    await expect(page.getByLabel('How to invoke forgejo-runner')).toContainText(`echo -n "${runnerToken}"`);
  });

  test('delete runner', async ({page}) => {
    await page.goto('/user/settings/actions/runners');

    const area = page.locator('.page-content');

    // We have to create a new runner because changes to fixtures affect the remainder of the tests in this file.
    await area.getByRole('link', {name: 'Create new runner'}).click();
    await area.getByRole('textbox', {name: 'Name *'}).fill('runner-660332');
    await area.getByRole('textbox', {name: 'Description'}).fill('Description of runner-660332');
    await area.getByRole('button', {name: 'Create'}).click();

    // Go back to list of runners.
    await area.getByRole('link', {name: 'Runners', exact: true}).click();
    const runnerList = page.locator('.runner-list');

    await expect(area.getByRole('heading', {name: 'Manage runners'})).toBeVisible();
    await expect(runnerList).toContainText('runner-660332');

    // Delete the runner that was just created.
    await runnerList.getByRole('button', {name: 'Delete runner-660332'}).click();

    // Confirm deletion
    const modal = page.locator('#runner-delete-modal');
    await expect(modal).toBeVisible();
    await expect(modal).toContainText('Confirm to delete this runner');

    await modal.getByRole('button', {name: 'Yes', exact: true}).click();

    // Verify that the runner is gone.
    await expect(area.locator('#flash-message')).toHaveText('Runner deleted successfully');
    await expect(runnerList).not.toContainText('runner-660332');
  });
});

test.describe('Global runners', () => {
  test.use({user: 'user1'});

  test('all runners are visible', async ({page}) => {
    await page.goto('/admin/actions/runners');

    await expect(page.getByRole('heading', {name: 'Manage runners'})).toBeVisible();

    const runnerContainer = page.locator('.runner-container');
    const rows = runnerContainer.getByRole('row');

    // We cannot assert the length of the table because it's influenced by global fixtures. It also changes depending on
    // the ordering of tests.
    await expect(rows.nth(0)).toHaveAccessibleName('Name Labels Type Status Edit Delete');
    await expect(page.locator('tbody tr:has-text("8f940b0b-32a2-479a-9d48-06ab8d8a0b90")')).toMatchAriaSnapshot(`
      - cell "runner-1 8f940b0b-32a2-479a-9d48-06ab8d8a0b90":
        - link "runner-1":
          - /url: /admin/actions/runners/719931
      - cell "debian gpu"
      - cell "Organization org3"
      - cell "Offline"
      - cell "Edit runner-1"
      - cell "Delete runner-1"
    `);
    await expect(page.locator('tbody tr:has-text("3a20ad8d-d5d6-4b7b-ba55-841ac8264c17")')).toMatchAriaSnapshot(`
      - cell "runner-2 3a20ad8d-d5d6-4b7b-ba55-841ac8264c17":
        - link "runner-2":
          - /url: /admin/actions/runners/719932
      - cell "docker"
      - cell "Individual user2"
      - cell "Offline"
      - cell "Edit runner-2"
      - cell "Delete runner-2"
    `);
    await expect(page.locator('tbody tr:has-text("11c9a6da-0a92-46ea-a4f1-b6c98f8c781c")')).toMatchAriaSnapshot(`
      - cell "runner-3 11c9a6da-0a92-46ea-a4f1-b6c98f8c781c":
        - link "runner-3":
          - /url: /admin/actions/runners/719933
      - cell "fedora"
      - cell "Organization org17"
      - cell "Offline"
      - cell "Edit runner-3"
      - cell "Delete runner-3"
    `);
    await expect(page.locator('tbody tr:has-text("1ef59b64-93b7-4ad4-ade4-21ca13db49c0")')).toMatchAriaSnapshot(`
      - cell "runner-4 1ef59b64-93b7-4ad4-ade4-21ca13db49c0":
        - link "runner-4":
          - /url: /admin/actions/runners/719934
      - cell "docker"
      - cell "Global"
      - cell "Offline"
      - cell "Edit runner-4"
      - cell "Delete runner-4"
    `);
    await expect(page.locator('tbody tr:has-text("69d29449-1de5-4d17-845d-e3ae11a04a1b")')).toMatchAriaSnapshot(`
      - cell "runner-5 69d29449-1de5-4d17-845d-e3ae11a04a1b":
        - link "runner-5":
          - /url: /admin/actions/runners/719935
      - cell "debian"
      - cell "Individual user1"
      - cell "Offline"
      - cell "Edit runner-5"
      - cell "Delete runner-5"
    `);
    await expect(page.locator('tbody tr:has-text("9da25fbb-89a5-4520-a35a-d55fc94e4b76")')).toMatchAriaSnapshot(`
      - cell "runner-6 9da25fbb-89a5-4520-a35a-d55fc94e4b76":
        - link "runner-6":
          - /url: /admin/actions/runners/719936
      - cell "debian"
      - cell "Repository user2/test_workflows"
      - cell "Offline"
      - cell "Edit runner-6"
      - cell "Delete runner-6"
    `);
    await expect(page.locator('tbody tr:has-text("d935307e-1d2d-4b61-8885-bc8a1c52c269")')).toMatchAriaSnapshot(`
      - cell "runner-7 d935307e-1d2d-4b61-8885-bc8a1c52c269":
        - link "runner-7":
          - /url: /admin/actions/runners/719937
      - cell "alpine"
      - cell "Individual user4"
      - cell "Offline"
      - cell "Edit runner-7"
      - cell "Delete runner-7"
    `);
  });

  test('runner details with all tasks visible on details page', async ({page}) => {
    await page.goto('/admin/actions/runners');

    await page.getByRole('link', {name: 'runner-4', exact: true}).click();

    await expect(page).toHaveTitle(/^Runner runner-4 .*/);
    await expect(page.getByRole('heading', {name: 'Runner runner-4'})).toBeVisible();

    await expect(page.getByLabel('Properties of runner-4')).toMatchAriaSnapshot(`
      - term: UUID
      - definition: ${uuidPattern}
      - term: Type
      - definition: Global
      - term: Labels
      - definition: docker
      - term: Last online time
      - definition: Never
      - term: Status
      - definition: Offline
      - term: Ephemeral
      - definition: "no"
      - term: Version
      - definition: 12.2.0
      - term: Description
      - definition: A runner for everyone
    `);

    await expect(page.getByRole('heading', {name: 'Recent tasks on this runner'})).toBeVisible();

    const rows = page.getByRole('row');

    // Only tasks from repositories owned by user2 should appear.
    await expect(rows).toHaveCount(3);
    await expect(rows.nth(0)).toHaveAccessibleName('Run Status Repository Commit Done at');
    await expect(rows.nth(1)).toHaveAccessibleName('88932 Waiting 49f55ab99b -');
    await expect(rows.nth(2)).toHaveAccessibleName('88931 Running ed38c5a46c -');
  });

  test('create new runner', async ({page}) => {
    await page.goto('/admin/actions/runners');

    const area = page.locator('.page-content');
    await expect(area.getByRole('heading', {name: 'Manage runners'})).toBeVisible();

    // Go to runner creation page
    await area.getByRole('link', {name: 'Create new runner'}).click();
    await expect(page).toHaveTitle(/^New runner .*/);

    // Submit an invalid form to test validation
    await area.getByRole('button', {name: 'Create'}).click();
    await expect(area.getByRole('paragraph')).toHaveText('Name cannot be empty.');

    // Submit a valid form to create a runner
    await area.getByRole('textbox', {name: 'Name *'}).fill('runner-473465');
    await area.getByRole('textbox', {name: 'Description'}).fill('Description of runner-473465');
    await area.getByRole('button', {name: 'Create'}).click();

    // Verify set up instructions
    await expect(page).toHaveTitle(/^Set up runner runner-473465 .*/);
    await expect(area.getByRole('heading', {name: 'Set up runner runner-473465'})).toBeVisible();

    let runnerUUID;
    await expect(async () => {
      await area.getByRole('button', {name: 'Copy runner UUID'}).click();
      runnerUUID = await page.evaluate(() => navigator.clipboard.readText());
      expect(runnerUUID).toMatch(uuidPattern);
    }).toPass();

    let runnerToken;
    await expect(async () => {
      await area.getByRole('button', {name: 'Copy runner token'}).click();
      runnerToken = await page.evaluate(() => navigator.clipboard.readText());
      expect(runnerToken).toMatch(tokenPattern);
    }).toPass();

    await expect(area.getByRole('term')).toHaveText(['UUID', 'Token']);
    await expect(area.getByRole('definition')).toContainText([runnerUUID, runnerToken]);

    await expect(area.getByRole('heading', {name: 'Using the runner configuration file'})).toBeVisible();
    await expect(area.getByLabel('Snippet to insert into the runner configuration')).toContainText(`uuid: ${runnerUUID}`);
    await expect(area.getByLabel('Snippet to insert into the runner configuration')).toContainText(`token: ${runnerToken}`);

    await expect(area.getByRole('heading', {name: 'Using program options'})).toBeVisible();
    await expect(area.getByLabel('How to invoke forgejo-runner')).toContainText(`--uuid ${runnerUUID}`);
    await expect(area.getByLabel('How to invoke forgejo-runner')).toContainText(`echo -n "${runnerToken}"`);

    // Go back to list of runners.
    await area.getByRole('link', {name: 'List of runners', exact: true}).click();

    await expect(area.locator(`tbody tr:has-text("${runnerUUID}")`)).toMatchAriaSnapshot(`
      - cell "runner-473465 ${runnerUUID}":
        - link "runner-473465":
          - /url: /admin/actions/runners/\\d+/
      - cell ""
      - cell "Global"
      - cell "Offline"
      - cell "Edit runner-473465"
      - cell "Delete runner-473465"
    `);
  });

  test('edit runner without changing its token', async ({page}) => {
    await page.goto('/admin/actions/runners');

    const area = page.locator('.page-content');

    // We have to create a new runner because changes to fixtures would affect the remainder of the tests in this file.
    await area.getByRole('link', {name: 'Create new runner'}).click();
    await area.getByRole('textbox', {name: 'Name *'}).fill('runner-956857');
    await area.getByRole('textbox', {name: 'Description'}).fill('Description of runner-956857');
    await area.getByRole('button', {name: 'Create'}).click();

    // Go back to list of runners.
    await area.getByRole('link', {name: 'Runners', exact: true}).click();

    // Edit the runner that was just created.
    await area.getByRole('link', {name: 'Edit runner-956857'}).click();

    await expect(page).toHaveTitle(/^Edit runner runner-956857 .*/);
    await expect(area.getByRole('heading', {name: 'Edit runner runner-956857'})).toBeVisible();

    // Make the form invalid to test validation.
    await area.getByRole('textbox', {name: 'Name *'}).clear();
    await area.getByRole('button', {name: 'Save'}).click();

    await expect(page.locator('#flash-message')).toHaveText('Name cannot be empty.');
    await expect(area.getByRole('textbox', {name: 'Name *'})).toBeEmpty();
    await expect(area.getByRole('textbox', {name: 'Description'})).toHaveValue('Description of runner-956857');

    // Submit a valid form.
    await area.getByRole('textbox', {name: 'Name *'}).fill('runner-956858');
    await area.getByRole('textbox', {name: 'Description'}).fill('Description of runner-956858');

    await area.getByRole('button', {name: 'Save'}).click();

    // Verify that the runner's properties were updated properly.
    await expect(page).toHaveTitle(/^Runner runner-956858 .*/);
    await expect(page.locator('#flash-message')).toHaveText('Runner edited successfully');
    await expect(area.getByRole('heading', {name: 'Runner runner-956858'})).toBeVisible();

    await expect(area.getByLabel('Properties of runner-956858')).toMatchAriaSnapshot(`
      - term: UUID
      - definition: ${uuidPattern}
      - term: Type
      - definition: Global
      - term: Labels
      - definition
      - term: Last online time
      - definition: Never
      - term: Status
      - definition: Offline
      - term: Ephemeral
      - definition: "no"
      - term: Version
      - definition: —
      - term: Description
      - definition: Description of runner-956858
    `);
  });

  test('delete runner', async ({page}) => {
    await page.goto('/admin/actions/runners');

    const area = page.locator('.page-content');

    // We have to create a new runner because changes to fixtures affect the remainder of the tests in this file.
    await area.getByRole('link', {name: 'Create new runner'}).click();
    await area.getByRole('textbox', {name: 'Name *'}).fill('runner-650332');
    await area.getByRole('textbox', {name: 'Description'}).fill('Description of runner-650332');
    await area.getByRole('button', {name: 'Create'}).click();

    // Go back to list of runners.
    await area.getByRole('link', {name: 'Runners', exact: true}).click();
    const runnerList = page.locator('.runner-list');

    await expect(area.getByRole('heading', {name: 'Manage runners'})).toBeVisible();
    await expect(runnerList).toContainText('runner-650332');

    // Delete the runner that was just created.
    await runnerList.getByRole('button', {name: 'Delete runner-650332'}).click();

    // Confirm deletion
    const modal = page.locator('#runner-delete-modal');
    await expect(modal).toBeVisible();
    await expect(modal).toContainText('Confirm to delete this runner');

    await modal.getByRole('button', {name: 'Yes', exact: true}).click();

    // Verify that the runner is gone.
    await expect(area.locator('#flash-message')).toHaveText('Runner deleted successfully');
    await expect(runnerList).not.toContainText('runner-650332');
  });
});

test.describe('Organization runners', () => {
  test.use({user: 'user2'});

  test('usable runners are visible', async ({page}) => {
    await page.goto('/org/org3/settings/actions/runners');

    await expect(page.getByRole('heading', {name: 'Manage runners'})).toBeVisible();

    const runnerContainer = page.locator('.runner-container');
    const rows = runnerContainer.getByRole('row');

    // We cannot assert the length of the table because it's influenced by global fixtures. It also changes depending on
    // the ordering of tests.
    await expect(rows.nth(0)).toHaveAccessibleName('Name Labels Type Status Edit Delete');
    await expect(page.locator('tbody tr:has-text("8f940b0b-32a2-479a-9d48-06ab8d8a0b90")')).toMatchAriaSnapshot(`
      - cell "runner-1 8f940b0b-32a2-479a-9d48-06ab8d8a0b90":
        - link "runner-1":
          - /url: /org/org3/settings/actions/runners/719931
      - cell "debian gpu"
      - cell "Organization"
      - cell "Offline"
      - cell "Edit runner-1"
      - cell "Delete runner-1"
    `);
    await expect(page.locator('tbody tr:has-text("1ef59b64-93b7-4ad4-ade4-21ca13db49c0")')).toMatchAriaSnapshot(`
      - cell "runner-4 1ef59b64-93b7-4ad4-ade4-21ca13db49c0":
        - link "runner-4":
          - /url: /org/org3/settings/actions/runners/719934
      - cell "docker"
      - cell "Global"
      - cell "Offline"
      - cell
      - cell
    `);
    await expect(page.locator('tbody tr:has-text("3a20ad8d-d5d6-4b7b-ba55-841ac8264c17")')).toBeHidden();
    await expect(page.locator('tbody tr:has-text("11c9a6da-0a92-46ea-a4f1-b6c98f8c781c")')).toBeHidden();
    await expect(page.locator('tbody tr:has-text("69d29449-1de5-4d17-845d-e3ae11a04a1b")')).toBeHidden();
    await expect(page.locator('tbody tr:has-text("9da25fbb-89a5-4520-a35a-d55fc94e4b76")')).toBeHidden();
    await expect(page.locator('tbody tr:has-text("d935307e-1d2d-4b61-8885-bc8a1c52c269")')).toBeHidden();

    // Verify that details of usable runners are accessible.
    await page.getByRole('link', {name: 'runner-1', exact: true}).click();
    await expect(page).toHaveTitle(/^Runner runner-1 .*/);

    await page.goto('/org/org3/settings/actions/runners');

    await page.getByRole('link', {name: 'runner-4', exact: true}).click();
    await expect(page).toHaveTitle(/^Runner runner-4 .*/);
  });

  test('runner details with tasks of repositories owned by organization', async ({page}) => {
    await page.goto('/org/org3/settings/actions/runners');

    await page.getByRole('link', {name: 'runner-4', exact: true}).click();

    await expect(page).toHaveTitle(/^Runner runner-4 .*/);
    await expect(page.getByRole('heading', {name: 'Runner runner-4'})).toBeVisible();

    await expect(page.getByLabel('Properties of runner-4')).toMatchAriaSnapshot(`
      - term: UUID
      - definition: ${uuidPattern}
      - term: Type
      - definition: Global
      - term: Labels
      - definition: docker
      - term: Last online time
      - definition: Never
      - term: Status
      - definition: Offline
      - term: Ephemeral
      - definition: "no"
      - term: Version
      - definition: 12.2.0
      - term: Description
      - definition: A runner for everyone
    `);

    await expect(page.getByRole('heading', {name: 'Recent tasks on this runner within this organization'})).toBeVisible();

    const rows = page.getByRole('row');

    // Only tasks from repositories owned by org3 should appear.
    await expect(rows).toHaveCount(2);
    await expect(rows.nth(0)).toHaveAccessibleName('Run Status Repository Commit Done at');
    await expect(rows.nth(1)).toHaveAccessibleName('88931 Running ed38c5a46c -');
  });

  test('runner details with multiple pages of tasks', async ({page}) => {
    await page.goto('/org/org3/settings/actions/runners');

    await page.getByRole('link', {name: 'runner-1', exact: true}).click();

    await expect(page).toHaveTitle(/^Runner runner-1 .*/);
    await expect(page.getByRole('heading', {name: 'Runner runner-1'})).toBeVisible();

    await expect(page.getByRole('heading', {name: 'Recent tasks on this runner within this organization'})).toBeVisible();

    const rows = page.getByRole('row');

    await expect(rows).toHaveCount(31); // 30 runners plus table header
    await expect(rows.nth(0)).toHaveAccessibleName('Run Status Repository Commit Done at');
    await expect(rows.nth(1)).toHaveAccessibleName('88930 Canceled ed4df76f86 -');
    await expect(rows.nth(30)).toHaveAccessibleName('88900 Success aa06c3e960 -');

    await page.getByRole('link', {name: 'Next', exact: true}).click();

    await expect(rows).toHaveCount(2);
    await expect(rows.nth(0)).toHaveAccessibleName('Run Status Repository Commit Done at');
    await expect(rows.nth(1)).toHaveAccessibleName('88899 Success d553d4419a -');

    // Go back to the first page and verify that nothing has changed.
    await page.getByRole('link', {name: 'Previous', exact: true}).click();

    await expect(rows).toHaveCount(31); // 30 runners plus table header
    await expect(rows.nth(0)).toHaveAccessibleName('Run Status Repository Commit Done at');
    await expect(rows.nth(1)).toHaveAccessibleName('88930 Canceled ed4df76f86 -');
    await expect(rows.nth(30)).toHaveAccessibleName('88900 Success aa06c3e960 -');
  });
});

test.describe('Repository runners', () => {
  test.use({user: 'user2'});

  test('usable runners are visible', async ({page}) => {
    await page.goto('/user2/test_workflows/settings/actions/runners');

    await expect(page.getByRole('heading', {name: 'Manage runners'})).toBeVisible();

    const runnerContainer = page.locator('.runner-container');
    const rows = runnerContainer.getByRole('row');

    // We cannot assert the length of the table because it's influenced by global fixtures. It also changes depending on
    // the ordering of tests.
    await expect(rows.nth(0)).toHaveAccessibleName('Name Labels Type Status Edit Delete');
    await expect(page.locator('tbody tr:has-text("3a20ad8d-d5d6-4b7b-ba55-841ac8264c17")')).toMatchAriaSnapshot(`
      - cell "runner-2 3a20ad8d-d5d6-4b7b-ba55-841ac8264c17":
        - link "runner-2":
          - /url: /user2/test_workflows/settings/actions/runners/719932
      - cell "docker"
      - cell "Individual"
      - cell "Offline"
      - cell
      - cell
    `);
    await expect(page.locator('tbody tr:has-text("1ef59b64-93b7-4ad4-ade4-21ca13db49c0")')).toMatchAriaSnapshot(`
      - cell "runner-4 1ef59b64-93b7-4ad4-ade4-21ca13db49c0":
        - link "runner-4":
          - /url: /user2/test_workflows/settings/actions/runners/719934
      - cell "docker"
      - cell "Global"
      - cell "Offline"
      - cell
      - cell
    `);
    await expect(page.locator('tbody tr:has-text("9da25fbb-89a5-4520-a35a-d55fc94e4b76")')).toMatchAriaSnapshot(`
      - cell "runner-6 9da25fbb-89a5-4520-a35a-d55fc94e4b76":
        - link "runner-6":
          - /url: /user2/test_workflows/settings/actions/runners/719936
      - cell "debian"
      - cell "Repository"
      - cell "Offline"
      - cell "Edit runner-6"
      - cell "Delete runner-6"
    `);
    await expect(page.locator('tbody tr:has-text("11c9a6da-0a92-46ea-a4f1-b6c98f8c781c")')).toBeHidden();
    await expect(page.locator('tbody tr:has-text("69d29449-1de5-4d17-845d-e3ae11a04a1b")')).toBeHidden();
    await expect(page.locator('tbody tr:has-text("d935307e-1d2d-4b61-8885-bc8a1c52c269")')).toBeHidden();

    // Verify that details of usable runners are accessible.
    await page.getByRole('link', {name: 'runner-2', exact: true}).click();
    await expect(page).toHaveTitle(/^Runner runner-2 .*/);

    await page.goto('/user2/test_workflows/settings/actions/runners');

    await page.getByRole('link', {name: 'runner-4', exact: true}).click();
    await expect(page).toHaveTitle(/^Runner runner-4 .*/);

    await page.goto('/user2/test_workflows/settings/actions/runners');

    await page.getByRole('link', {name: 'runner-6', exact: true}).click();
    await expect(page).toHaveTitle(/^Runner runner-6 .*/);
  });

  test('runner details with tasks of repository only', async ({page}) => {
    await page.goto('/user2/test_workflows/settings/actions/runners');

    await page.getByRole('link', {name: 'runner-4', exact: true}).click();

    await expect(page).toHaveTitle(/^Runner runner-4 .*/);
    await expect(page.getByRole('heading', {name: 'Runner runner-4'})).toBeVisible();

    await expect(page.getByLabel('Properties of runner-4')).toMatchAriaSnapshot(`
      - term: UUID
      - definition: ${uuidPattern}
      - term: Type
      - definition: Global
      - term: Labels
      - definition: docker
      - term: Last online time
      - definition: Never
      - term: Status
      - definition: Offline
      - term: Ephemeral
      - definition: "no"
      - term: Version
      - definition: 12.2.0
      - term: Description
      - definition: A runner for everyone
    `);

    await expect(page.getByRole('heading', {name: 'Recent tasks of this repository on this runner'})).toBeVisible();

    const rows = page.getByRole('row');

    // Only tasks from this repository should appear.
    await expect(rows).toHaveCount(2);
    await expect(rows.nth(0)).toHaveAccessibleName('Run Status Repository Commit Done at');
    await expect(rows.nth(1)).toHaveAccessibleName('There are no tasks yet.');
  });
});

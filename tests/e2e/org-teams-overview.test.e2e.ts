// @watch start
// templates/org/team/sidebar.tmpl
// @watch end
/* eslint playwright/expect-expect: ["error", { "assertFunctionNames": ["assertPermissionsDetails", "assertRestrictedAccess", "assertOwnerPermissions"] }] */
import {expect, type Page} from '@playwright/test';
import {test} from './utils_e2e.ts';

type Permission = 'No access' | 'Write' | 'Read';

const UNIT_VALUES = [
  'Code',
  'Issues',
  'Pull requests',
  'Releases',
  'Wiki',
  'External Wiki',
  'External issues',
  'Projects',
  'Packages',
  'Actions',
] as const;

type Unit = typeof UNIT_VALUES[number];

const assertPermission = async (page: Page, name: Unit, permission: Permission) => {
  await expect.soft(page.getByRole('row', {name}).getByRole('cell').nth(1)).toHaveText(permission);
};

const testTeamUrl = '/org/org17/teams/test_team';
const reviewTeamUrl = '/org/org17/teams/review_team';
const ownersUrl = '/org/org17/teams/owners';
const adminUrl = '/org/org17/teams/super-user';

const cases: Record<string, { read?: Unit[], write?: Unit[] }> = {
  [testTeamUrl]: {write: ['Issues']},
  [reviewTeamUrl]: {read: ['Code']},
};

const assertOwnerPermissions = async (page: Page, code: number = 200) => {
  const response = await page.goto(ownersUrl);
  expect(response?.status()).toBe(code);

  await expect(page.getByText('Owners have full access to all repositories and have administrator access to the organization.')).toBeVisible();
};

const assertAdminPermissions = async (page: Page, code: number = 200) => {
  const response = await page.goto(adminUrl);
  expect(response?.status()).toBe(code);

  await expect(page.getByText('This team grants Administrator access: members can read from, push to and add collaborators to team repositories.')).toBeVisible();
};

const assertRestrictedAccess = async (page: Page, ...urls: string[]) => {
  for (const url of urls) {
    expect((await page.goto(url))?.status(), 'should not see any details').toBe(404);
  }
};

const assertPermissionsDetails = async (page: Page, url: (keyof typeof cases)) => {
  const response = await page.goto(url);
  expect(response?.status()).toBe(200);

  const per = cases[url];

  for (const unit of UNIT_VALUES) {
    if (per.read?.includes(unit)) {
      await assertPermission(page, unit, 'Read');
    } else if (per.write?.includes(unit)) {
      await assertPermission(page, unit, 'Write');
    } else {
      await assertPermission(page, unit, 'No access');
    }
  }
};

test.describe('Orga team overview', () => {
  test.describe('admin', () => {
    test.use({user: 'user1'});

    test('should see all', async ({page}) => {
      await assertPermissionsDetails(page, testTeamUrl);
      await assertPermissionsDetails(page, reviewTeamUrl);
      await assertOwnerPermissions(page);
      await assertAdminPermissions(page);
    });
  });

  test.describe('owner', () => {
    test.use({user: 'user18'});

    test('should see all', async ({page}) => {
      await assertPermissionsDetails(page, testTeamUrl);
      await assertPermissionsDetails(page, reviewTeamUrl);
      await assertOwnerPermissions(page);
      await assertAdminPermissions(page);
    });
  });

  test.describe('reviewer team', () => {
    test.use({user: 'user29'});

    test('should only see permissions for `reviewer team` and restricted access to other resources', async ({page}) => {
      await assertPermissionsDetails(page, reviewTeamUrl);
      await assertRestrictedAccess(page, ownersUrl, testTeamUrl, adminUrl);
    });
  });

  test.describe('test_team', () => {
    test.use({user: 'user2'});

    test('should only see permissions for test_team and restricted access to other resources', async ({page}) => {
      await assertPermissionsDetails(page, testTeamUrl);
      await assertRestrictedAccess(page, ownersUrl, reviewTeamUrl, adminUrl);
    });
  });
});

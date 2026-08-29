import {expect, test as baseTest, type Browser, type BrowserContextOptions, type APIRequestContext, type TestInfo} from '@playwright/test';

import * as path from 'node:path';

const AUTH_PATH = 'tests/e2e/.auth';

type AuthScope = 'logout' | 'shared' | 'webauthn';

export type TestOptions = {
  forEachTest: void
  user: string | null;
  authScope: AuthScope;
};

export const test = baseTest.extend<TestOptions>({
  context: async ({browser, user, authScope, contextOptions}, use, {project}) => {
    if (user && authScope) {
      const browserName = project.name.toLowerCase().replace(' ', '-');
      contextOptions.storageState = path.join(AUTH_PATH, `state-${browserName}-${user}-${authScope}.json`);
    } else {
      // if no user is given, ensure to have clean state
      contextOptions.storageState = {cookies: [], origins: []};
    }

    return use(await test_context(browser, contextOptions));
  },
  user: null,
  authScope: 'shared',
});

export async function test_context(browser: Browser, options?: BrowserContextOptions) {
  const context = await browser.newContext(options);

  context.on('page', (page) => {
    page.on('pageerror', (err) => expect(err).toBeUndefined());
  });

  return context;
}

const ARTIFACTS_PATH = `tests/e2e/test-artifacts`;
const LOGIN_PASSWORD = 'password';

// log in user and store session info. This should generally be
//  run in test.beforeAll(), then the session can be loaded in tests.
export async function login_user(browser: Browser, workerInfo: TestInfo, user: string, options?: BrowserContextOptions) {
  test.setTimeout(60000);
  // Set up a new context
  const context = await test_context(browser, options);
  const page = await context.newPage();

  // Route to login page
  // Note: this could probably be done more quickly with a POST
  const response = await page.goto('/user/login');
  expect(response?.status()).toBe(200); // Status OK

  // Fill out form
  await page.fill('input[name=user_name]', user);
  await page.fill('input[name=password]', LOGIN_PASSWORD);
  await page.click('form button.ui.primary.button:visible');

  await page.waitForLoadState();

  expect(page.url(), {message: `Failed to login user ${user}`}).toBe(`${workerInfo.project.use.baseURL}/`);

  // Save state
  await context.storageState({path: `${ARTIFACTS_PATH}/state-${user}-${workerInfo.workerIndex}.json`});

  return context;
}

export async function load_logged_in_context(browser: Browser, workerInfo: TestInfo, user: string) {
  try {
    return await test_context(browser, {storageState: `${ARTIFACTS_PATH}/state-${user}-${workerInfo.workerIndex}.json`});
  } catch (err) {
    if (err.code === 'ENOENT') {
      throw new Error(`Could not find state for '${user}'. Did you call login_user(browser, workerInfo, '${user}') in test.beforeAll()?`);
    }
  }
}

export async function login({browser}: {browser: Browser}, workerInfo: TestInfo) {
  return await login_as_user(browser, workerInfo, 'user2');
}

export async function login_as_user(browser: Browser, workerInfo: TestInfo, user: string) {
  const context = await load_logged_in_context(browser, workerInfo, user);
  return await context?.newPage();
}

// Create a temporary user and login to that user and store session info.
// This should ideally run on a per test basis.
export async function create_temp_user(browser: Browser, workerInfo: TestInfo, request: APIRequestContext) {
  const username = globalThis.crypto.randomUUID();
  const newUser = await request.post(`/api/v1/admin/users`, {
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Basic ${btoa(`user1:${LOGIN_PASSWORD}`)}`,
    },
    data: {
      username,
      email: `${username}@host.invalid`,
      password: LOGIN_PASSWORD,
      must_change_password: false,
    },
  });
  expect(newUser.ok()).toBeTruthy();

  return {context: await login_user(browser, workerInfo, username), username};
}

// returns a random string with a pattern that can be filtered for screenshots automatically
export function dynamic_id() {
  return `dyn-id-${globalThis.crypto.randomUUID()}`;
}

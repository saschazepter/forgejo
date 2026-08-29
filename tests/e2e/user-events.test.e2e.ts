// @watch start
// templates/base/head_navbar.tmpl
// templates/base/head_script.tmpl
// web_src/js/features/notification.js
// web_src/js/features/eventsource.sharedworker.js
// @watch end

import {expect} from '@playwright/test';
import {test, dynamic_id, login_user, login_as_user} from './utils_e2e.ts';

test('Notifications', async ({browser}, workerInfo) => {
  await login_user(browser, workerInfo, 'user2');

  const pageUser2 = await login_as_user(browser, workerInfo, 'user2');
  await pageUser2.goto(`/`);
  const originalNotifications = parseInt(await pageUser2.locator('.not-mobile .notification_count').textContent());

  await login_user(browser, workerInfo, 'user1');
  const pageUser1 = await login_as_user(browser, workerInfo, 'user1');
  const response = await pageUser1.goto('/user2/mentions-highlighted/issues/new');
  expect(response?.status()).toBe(200);
  const issueTitle = dynamic_id();
  const issueContent = dynamic_id();
  await pageUser1.getByPlaceholder('Title').fill(issueTitle);
  await pageUser1.getByPlaceholder('Leave a comment').fill(issueContent);
  await pageUser1.getByRole('button', {name: 'Create issue'}).click();
  await expect(pageUser1).toHaveURL(/\/user2\/mentions-highlighted\/issues\/\d+$/);

  await expect(async () => {
    const modifiedNotifications = parseInt(await pageUser2.locator('.not-mobile .notification_count').textContent());
    expect(modifiedNotifications - originalNotifications).toBe(1);
  }).toPass({
    // [ui.notification].EVENT_SOURCE_UPDATE_TIME = 1s
    intervals: [1_000],
    timeout: 60_000,
  });
});

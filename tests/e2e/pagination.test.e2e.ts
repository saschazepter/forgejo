// @watch start
// templates/base/paginate.tmpl
// services/context/pagination.go
// @watch end

import {expect} from '@playwright/test';
import {test} from './utils_e2e.ts';
import {accessibilityCheck} from './shared/accessibility.ts';

test('Pagination a11y', async ({page}) => {
  await page.goto('/explore/repos');

  await expect(page.locator('.pagination')).toBeVisible();
  await accessibilityCheck({page}, ['.pagination'], [], []);
});

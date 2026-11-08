import $ from 'jquery';
import {minimatch} from 'minimatch';
import {onInputDebounce, toggleElem} from '../utils/dom.ts';
import {POST} from '../modules/fetch.ts';
import {createCodemirror} from './codemirror.ts';

const {appSubUrl, i18n} = window.config;

export function initRepoSettingsCollaboration() {
  // Change collaborator access mode
  for (const dropdownEl of document.querySelectorAll('.page-content.repository .dialog-dropdown.access-mode')) {
    const url = dropdownEl.getAttribute('data-url');
    const uid = dropdownEl.getAttribute('data-uid');
    const textEl = dropdownEl.querySelector('.text');
    for (const buttonEl of dropdownEl.querySelectorAll('button[data-mode]')) {
      const mode = buttonEl.getAttribute('data-mode');
      buttonEl.addEventListener('click', async () => {
        dropdownEl.classList.add('is-loading');
        dropdownEl.removeAttribute('open');

        const data = new FormData();
        data.append('uid', uid);
        data.append('mode', mode);

        try {
          await POST(url, {data});
          textEl.textContent = buttonEl.textContent;
        } catch {
          showErrorToast(i18n.network_error);
        }
        dropdownEl.classList.remove('is-loading');
      }, {passive: true});
    }
  }
}

export function initRepoSettingSearchTeamBox() {
  const searchTeamBox = document.getElementById('search-team-box');
  if (!searchTeamBox) return;

  $(searchTeamBox).search({
    minCharacters: 2,
    apiSettings: {
      url: `${appSubUrl}/org/${searchTeamBox.getAttribute('data-org-name')}/teams/-/search?q={query}`,
      onResponse(response) {
        const items = [];
        for (const item of response.data) {
          items.push({
            title: item.name,
            description: `${item.permission} access`, // TODO: translate this string
          });
        }
        return {results: items};
      },
    },
    searchFields: ['name', 'description'],
    showNoResults: false,
  });
}

export function initRepoSettingGitHook() {
  if (!$('.edit.githook').length) return;
  const filename = document.querySelector('.hook-filename').textContent;
  const _promise = createCodemirror($('#content')[0], filename, {language: 'shell'});
}

export function initRepoSettingBranches() {
  if (!document.querySelector('.repository.settings.branches')) return;

  for (const el of document.getElementsByClassName('toggle-target-enabled')) {
    el.addEventListener('change', function () {
      const target = document.querySelector(this.getAttribute('data-target'));
      target?.classList.toggle('disabled', !this.checked);
    });
  }

  for (const el of document.getElementsByClassName('toggle-target-disabled')) {
    el.addEventListener('change', function () {
      const target = document.querySelector(this.getAttribute('data-target'));
      if (this.checked) target?.classList.add('disabled'); // only disable, do not auto enable
    });
  }

  document.getElementById('dismiss_stale_approvals')?.addEventListener('change', function () {
    document.getElementById('ignore_stale_approvals_box')?.classList.toggle('disabled', this.checked);
  });

  // show the `Matched` mark for the status checks that match the pattern
  const markMatchedStatusChecks = () => {
    const patterns = (document.getElementById('status_check_contexts').value || '').split(/[\r\n]+/);
    const validPatterns = patterns.map((item) => item.trim()).filter(Boolean);
    const marks = document.getElementsByClassName('status-check-matched-mark');

    for (const el of marks) {
      let matched = false;
      const statusCheck = el.getAttribute('data-status-check');
      for (const pattern of validPatterns) {
        if (minimatch(statusCheck, pattern)) {
          matched = true;
          break;
        }
      }
      toggleElem(el, matched);
    }
  };
  markMatchedStatusChecks();
  document.getElementById('status_check_contexts').addEventListener('input', onInputDebounce(markMatchedStatusChecks));
}

export function initRepoSettingLfsPointers() {
  const lfsFilesTable = document.getElementById('lfs-files-table');
  if (!lfsFilesTable) return;

  const deleteButton = document.getElementById('lfs-files-delete-button');

  const oidCheckboxes = lfsFilesTable.querySelectorAll('input[type=checkbox][name="oid"]');
  const selectAllCheckboxes = lfsFilesTable.querySelectorAll('input[type=checkbox].select-all');
  const allCheckboxes = [...selectAllCheckboxes, ...oidCheckboxes];
  for (const selectAllCheckbox of selectAllCheckboxes) {
    const nameOfTarget = selectAllCheckbox.getAttribute('data-select-all-name');
    if (!nameOfTarget) continue;

    const allTargetedCheckboxes = allCheckboxes.filter((cb) => !cb.disabled && cb.name === nameOfTarget);

    function updateSelectAllCheckboxState() {
      let allChecked = true;
      let allUnchecked = true;
      for (const cb of allTargetedCheckboxes) {
        if (!cb.checked) {
          allChecked = false;
        } else {
          allUnchecked = false;
        }
      }
      if (allTargetedCheckboxes.length === 0) {
        allChecked = false;
      }

      selectAllCheckbox.checked = allChecked;
      selectAllCheckbox.indeterminate = !allChecked && !allUnchecked;

      if (deleteButton) {
        deleteButton.disabled = allUnchecked;
      }
    }

    selectAllCheckbox.addEventListener('change', (_e) => {
      for (const cb of allTargetedCheckboxes) {
        cb.checked = selectAllCheckbox.checked;
      }
      updateSelectAllCheckboxState();
    });

    for (const cb of allTargetedCheckboxes) {
      cb.addEventListener('change', (_e) => {
        updateSelectAllCheckboxState();
      });
    }

    updateSelectAllCheckboxState();
  }
}

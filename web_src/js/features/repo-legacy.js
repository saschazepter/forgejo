import $ from 'jquery';
import {
  initRepoIssueBranchSelect, initRepoIssueCodeCommentCancel, initRepoIssueCommentDelete,
  initRepoIssueComments, initRepoIssueDependencyDelete, initRepoIssueReferenceIssue,
  initRepoIssueTitleEdit, initRepoIssueWipToggle,
  initRepoPullRequestUpdate, updateIssuesMeta, handleReply, initIssueTemplateCommentEditors, initSingleCommentEditor,
  initRepoIssueAssignMe, reloadConfirmDraftComment,
} from './repo-issue.js';
import {initUnicodeEscapeButton} from './repo-unicode-escape.js';
import {svg} from '../svg.ts';
import {htmlEscape} from 'escape-goat';
import {initRepoBranchTagSelector} from './repo-branch-tag-selector.js';
import {
  initRepoCloneLink, initRepoCommonBranchOrTagDropdown, initRepoCommonFilterSearchDropdown,
} from './repo-common.js';
import {initCompLabelEdit} from './comp/LabelEdit.js';
import {initRepoDiffConversationNav} from './repo-diff.js';
import {syncSuggestionBatchUI} from './repo-suggestion.js';
import {showErrorToast} from '../modules/toast.ts';
import {initCommentContent, initMarkupContent} from '../markup/content.js';
import {initCompReactionSelector} from './comp/ReactionSelector.js';
import {initRepoSettingBranches, initRepoSettingLfsPointers} from './repo-settings.js';
import {initRepoPullRequestMergeForm} from './repo-issue-pr-form.js';
import {initRepoPullRequestCommitStatus} from './repo-issue-pr-status.js';
import {hideElem, showElem} from '../utils/dom.ts';
import {getComboMarkdownEditor, initComboMarkdownEditor} from './comp/ComboMarkdownEditor.js';
import {attachRefIssueContextPopup} from './contextpopup.js';
import {POST} from '../modules/fetch.ts';
import {MarkdownQuote} from '@github/quote-selection';
import {toAbsoluteUrl} from '../utils.ts';
import {initDropzone, initDisabledInputs} from './common-global.js';

export function initRepoCommentForm() {
  const $commentForm = $('.comment.form');
  if (!$commentForm.length) return;

  if ($commentForm.find('.field.combo-editor-dropzone').length) {
    // at the moment, if a form has multiple combo-markdown-editors, it must be an issue template form
    initIssueTemplateCommentEditors($commentForm);
  } else if ($commentForm.find('.combo-markdown-editor').length) {
    // it's quite unclear about the "comment form" elements, sometimes it's for issue comment, sometimes it's for file editor/uploader message
    initSingleCommentEditor($commentForm);
  }

  function initBranchSelector() {
    const $selectBranch = $('.ui.select-branch');
    const $branchMenu = $selectBranch.find('.reference-list-menu');
    const $isNewIssue = $branchMenu[0]?.classList.contains('new-issue');
    $branchMenu.find('.item:not(.no-select)').on('click', async function () {
      const selectedValue = $(this).data('id');
      const editMode = $('#editing_mode').val();
      $($(this).data('id-selector')).val(selectedValue);
      if ($isNewIssue) {
        $selectBranch.find('.ui .branch-name').text($(this).data('name'));
        return;
      }

      if (editMode === 'true') {
        const form = document.getElementById('update_issueref_form');
        const params = new URLSearchParams();
        params.append('ref', selectedValue);
        try {
          await POST(form.getAttribute('action'), {data: params});
          window.location.reload();
        } catch (error) {
          console.error(error);
        }
      } else if (editMode === '') {
        $selectBranch.find('.ui .branch-name').text(selectedValue);
      }
    });
    $selectBranch.find('.branch-tag-item').on('click', function () {
      hideElem($selectBranch.find('.scrolling.reference-list-menu'));
      $selectBranch.find('.reference .text').removeClass('black');
      showElem($($(this).data('target')));
      $(this).find('.text').addClass('black');
      return false;
    });
  }

  initBranchSelector();

  // List submits
  function initListSubmits(selector, outerSelector) {
    const $list = $(`.ui.${outerSelector}.list`);
    const $noSelect = $list.find('.no-select');
    const $listMenu = $(`.${selector} .menu`);
    let hasUpdateAction = $listMenu.data('action') === 'update';
    const items = {};

    $(`.${selector}`).dropdown({
      'action': 'nothing', // do not hide the menu if user presses Enter
      fullTextSearch: 'exact',
      selector: {
        unselectable: '.disabled, .filtered, .tw-hidden',
      },
      async onHide() {
        hasUpdateAction = $listMenu.data('action') === 'update'; // Update the var
        if (hasUpdateAction) {
          // TODO: Add batch functionality and make this 1 network request.
          const itemEntries = Object.entries(items);
          for (const [elementId, item] of itemEntries) {
            await updateIssuesMeta(
              item['update-url'],
              item.action,
              item['issue-id'],
              elementId,
            );
          }
          if (itemEntries.length) {
            reloadConfirmDraftComment();
          }
        }
      },
    });

    $listMenu.find('.item:not(.no-select)').on('click', function (e) {
      e.preventDefault();
      if (this.classList.contains('ban-change')) {
        return false;
      }

      hasUpdateAction = $listMenu.data('action') === 'update'; // Update the var

      const clickedItem = this; // eslint-disable-line unicorn/no-this-assignment, @typescript-eslint/no-this-alias
      const scope = this.getAttribute('data-scope');

      $(this).parent().find('.item').each(function () {
        if (scope) {
          // Enable only clicked item for scoped labels
          if (this.getAttribute('data-scope') !== scope) {
            return true;
          }
          if (this !== clickedItem && !this.classList.contains('checked')) {
            return true;
          }
        } else if (this !== clickedItem) {
          // Toggle for other labels
          return true;
        }

        if (this.classList.contains('checked')) {
          $(this).removeClass('checked');
          $(this).find('.octicon-check').addClass('tw-invisible');
          if (hasUpdateAction) {
            if (!($(this).data('id') in items)) {
              items[$(this).data('id')] = {
                'update-url': $listMenu.data('update-url'),
                action: 'detach',
                'issue-id': $listMenu.data('issue-id'),
              };
            } else {
              delete items[$(this).data('id')];
            }
          }
        } else {
          $(this).addClass('checked');
          $(this).find('.octicon-check').removeClass('tw-invisible');
          if (hasUpdateAction) {
            if (!($(this).data('id') in items)) {
              items[$(this).data('id')] = {
                'update-url': $listMenu.data('update-url'),
                action: 'attach',
                'issue-id': $listMenu.data('issue-id'),
              };
            } else {
              delete items[$(this).data('id')];
            }
          }
        }
      });

      // TODO: Which thing should be done for choosing review requests
      // to make chosen items be shown on time here?
      if (selector === 'select-reviewers-modify' || selector === 'select-assignees-modify') {
        return false;
      }

      const listIds = [];
      $(this).parent().find('.item').each(function () {
        if (this.classList.contains('checked')) {
          listIds.push($(this).data('id'));
          $($(this).data('id-selector')).removeClass('tw-hidden');
        } else {
          $($(this).data('id-selector')).addClass('tw-hidden');
        }
      });
      if (!listIds.length) {
        $noSelect.removeClass('tw-hidden');
      } else {
        $noSelect.addClass('tw-hidden');
      }
      $($(this).parent().data('id')).val(listIds.join(','));
      return false;
    });
    $listMenu.find('.no-select.item').on('click', function (e) {
      e.preventDefault();
      if (hasUpdateAction) {
        (async () => {
          await updateIssuesMeta(
            $listMenu.data('update-url'),
            'clear',
            $listMenu.data('issue-id'),
            '',
          );
          reloadConfirmDraftComment();
        })();
      }

      $(this).parent().find('.item').each(function () {
        $(this).removeClass('checked');
        $(this).find('.octicon-check').addClass('tw-invisible');
      });

      if (selector === 'select-reviewers-modify' || selector === 'select-assignees-modify') {
        return false;
      }

      $list.find('.item').each(function () {
        $(this).addClass('tw-hidden');
      });
      $noSelect.removeClass('tw-hidden');
      $($(this).parent().data('id')).val('');
    });
  }

  // Init labels and assignees
  initListSubmits('select-label', 'labels');
  initListSubmits('select-assignees', 'assignees');
  initRepoIssueAssignMe();
  initListSubmits('select-assignees-modify', 'assignees');
  initListSubmits('select-reviewers-modify', 'assignees');

  function selectItem(select_id, input_id) {
    const $menu = $(`${select_id} .menu`);
    const $list = $(`.ui${select_id}.list`);
    const hasUpdateAction = $menu.data('action') === 'update';

    $menu.find('.item:not(.no-select)').on('click', function () {
      $(this).parent().find('.item').each(function () {
        $(this).removeClass('selected active');
      });

      $(this).addClass('selected active');
      if (hasUpdateAction) {
        (async () => {
          await updateIssuesMeta(
            $menu.data('update-url'),
            '',
            $menu.data('issue-id'),
            $(this).data('id'),
          );
          reloadConfirmDraftComment();
        })();
      }

      let icon = '';
      if (input_id === '#milestone_id') {
        icon = svg('octicon-milestone', 18, 'tw-mr-2');
      } else if (input_id === '#project_id') {
        icon = svg('octicon-project', 18, 'tw-mr-2');
      } else if (input_id === '#assignee_ids') {
        icon = `<img class="ui avatar image tw-mr-2" alt="avatar" src=${$(this).data('avatar')}>`;
      }

      $list.find('.selected').html(`
        <a class="item muted sidebar-item-link" href=${$(this).data('href')}>
          ${icon}
          ${htmlEscape($(this).text())}
        </a>
      `);

      $(`.ui${select_id}.list .no-select`).addClass('tw-hidden');
      $(input_id).val($(this).data('id'));
    });
    $menu.find('.no-select.item').on('click', function () {
      $(this).parent().find('.item:not(.no-select)').each(function () {
        $(this).removeClass('selected active');
      });

      if (hasUpdateAction) {
        (async () => {
          await updateIssuesMeta(
            $menu.data('update-url'),
            '',
            $menu.data('issue-id'),
            $(this).data('id'),
          );
          reloadConfirmDraftComment();
        })();
      }

      $list.find('.selected').html('');
      $list.find('.no-select').removeClass('tw-hidden');
      $(input_id).val('');
    });
  }

  // Milestone, Assignee, Project
  selectItem('.select-project', '#project_id');
  selectItem('.select-milestone', '#milestone_id');
  selectItem('.select-assignee', '#assignee_ids');
}

async function onEditContent(event) {
  event.preventDefault();

  const segment = this.closest('.header').nextElementSibling;
  const editContentZone = segment.querySelector('.edit-content-zone');
  const renderContent = segment.querySelector('.render-content');
  const rawContent = segment.querySelector('.raw-content');

  let comboMarkdownEditor;

  const cancelAndReset = (e) => {
    e.preventDefault();
    showElem(renderContent);
    hideElem(editContentZone);
    comboMarkdownEditor.value(rawContent.textContent);
    editContentZone.querySelector('.dropzone')?.dropzone?.emit('reload');
  };

  const saveAndRefresh = async (e) => {
    e.preventDefault();
    showElem(renderContent);
    hideElem(editContentZone);
    const dropzone = editContentZone.querySelector('.dropzone')?.dropzone;
    for (const element of dropzone?.element?.querySelectorAll('.dz-preview') ?? []) element.classList.remove('dz-success');
    try {
      const params = new URLSearchParams({
        content: comboMarkdownEditor.value(),
        context: editContentZone.getAttribute('data-context'),
        content_version: editContentZone.getAttribute('data-content-version'),
      });
      // tells the server which batch button to render with the refreshed suggestion (Files tab vs Conversation)
      const batchMode = editContentZone.getAttribute('data-batch-mode');
      if (batchMode) params.append('batch_mode', batchMode);
      const files = dropzone?.element?.querySelectorAll('.files [name=files]') ?? [];
      for (const fileInput of files) {
        params.append('files[]', fileInput.value);
      }

      const response = await POST(editContentZone.getAttribute('data-update-url'), {data: params});
      const data = await response.json();
      if (response.status === 400) {
        showErrorToast(data.errorMessage);
        return;
      }
      editContentZone.setAttribute('data-content-version', data.contentVersion);
      if (!data.content) {
        renderContent.innerHTML = document.getElementById('no-content').innerHTML;
        rawContent.textContent = '';
      } else {
        renderContent.innerHTML = data.content;
        rawContent.textContent = comboMarkdownEditor.value();
        const refIssues = renderContent.querySelectorAll('p .ref-issue');
        attachRefIssueContextPopup(refIssues);
      }
      // refresh the server-rendered suggestion diff(s) so an added/changed suggestion shows without a reload
      for (const old of segment.querySelectorAll('.suggestion-diff')) old.remove();
      if (data.suggestions) {
        renderContent.insertAdjacentHTML('afterend', data.suggestions);
        syncSuggestionBatchUI(); // re-apply batch state to the freshly injected buttons
      }
      const content = segment;
      if (!content.querySelector('.dropzone-attachments')) {
        if (data.attachments !== '') {
          content.insertAdjacentHTML('beforeend', data.attachments);
        }
      } else if (data.attachments === '') {
        content.querySelector('.dropzone-attachments').remove();
      } else {
        content.querySelector('.dropzone-attachments').outerHTML = data.attachments;
      }
      dropzone?.emit('submit');
      initMarkupContent();
      initCommentContent();
    } catch (error) {
      console.error(error);
    }
  };

  comboMarkdownEditor = getComboMarkdownEditor(editContentZone.querySelector('.combo-markdown-editor'));
  if (!comboMarkdownEditor) {
    editContentZone.innerHTML = document.getElementById('issue-comment-editor-template').innerHTML;
    // the suggestion toolbar button only makes sense for a proposed-side code comment; hide it otherwise
    if (!editContentZone.hasAttribute('data-suggestion-line')) {
      editContentZone.querySelector('button[data-md-action="suggestion"]')?.classList.add('tw-hidden');
    }
    initDisabledInputs(editContentZone);
    const dropzone = editContentZone.querySelector('.dropzone');
    if (dropzone && !dropzone.dropzone) await initDropzone(dropzone, editContentZone);
    comboMarkdownEditor = await initComboMarkdownEditor(editContentZone.querySelector('.combo-markdown-editor'));
    dropzone?.dropzone?.emit('reload');
    editContentZone.addEventListener('ce-quick-submit', saveAndRefresh);
    editContentZone.querySelector('button[data-button-name="cancel-edit"]').addEventListener('click', cancelAndReset);
    editContentZone.querySelector('button[data-button-name="save-edit"]').addEventListener('click', saveAndRefresh);
  } else {
    const tabEditor = editContentZone.querySelector('.combo-markdown-editor').querySelector('.switch > [data-tab-for=markdown-writer]');
    tabEditor?.click();
  }

  // Show write/preview tab and copy raw content as needed
  showElem(editContentZone);
  hideElem(renderContent);
  if (!comboMarkdownEditor.value()) {
    comboMarkdownEditor.value(rawContent.textContent);
  }
  comboMarkdownEditor.focus();
}

export function initRepository() {
  if (!$('.page-content.repository').length) return;

  initRepoBranchTagSelector('.js-branch-tag-selector');

  // Options
  if ($('.repository.settings.options').length > 0) {
    // Enable or select internal/external wiki system and issue tracker.
    $('.enable-system').on('change', function () {
      if (this.checked) {
        $($(this).data('target')).removeClass('disabled');
        if (!$(this).data('context')) $($(this).data('context')).addClass('disabled');
      } else {
        $($(this).data('target')).addClass('disabled');
        if (!$(this).data('context')) $($(this).data('context')).removeClass('disabled');
      }
    });
    $('.enable-system-radio').on('change', function () {
      if (this.value === 'false') {
        $($(this).data('target')).addClass('disabled');
        if ($(this).data('context') !== undefined) $($(this).data('context')).removeClass('disabled');
      } else if (this.value === 'true') {
        $($(this).data('target')).removeClass('disabled');
        if ($(this).data('context') !== undefined) $($(this).data('context')).addClass('disabled');
      }
    });
    const $trackerIssueStyleRadios = $('.js-tracker-issue-style');
    $trackerIssueStyleRadios.on('change input', () => {
      const checkedVal = $trackerIssueStyleRadios.filter(':checked').val();
      $('#tracker-issue-style-regex-box').toggleClass('disabled', checkedVal !== 'regexp');
    });
  }

  // Labels
  initCompLabelEdit('.repository.labels');

  // Milestones
  if ($('.repository.new.milestone').length > 0) {
    $('#clear-date').on('click', () => {
      $('#deadline').val('');
      return false;
    });
  }

  // Repo Creation
  if ($('.repository.new.repo').length > 0) {
    $('input[name="gitignores"], input[name="license"]').on('change', () => {
      const gitignores = $('input[name="gitignores"]').val();
      const license = $('input[name="license"]').val();
      if (gitignores || license) {
        document.querySelector('input[name="auto_init"]').checked = true;
      }
    });
  }

  // Compare or pull request
  const $repoDiff = $('.repository.diff');
  if ($repoDiff.length) {
    initRepoCommonBranchOrTagDropdown('.choose.branch .dropdown');
    initRepoCommonFilterSearchDropdown('.choose.branch .dropdown');
  }

  initRepoCloneLink();
  initRepoSettingBranches();
  initRepoSettingLfsPointers();

  // Issues
  if ($('.repository.view.issue').length > 0) {
    initRepoIssueCommentEdit();

    initRepoIssueBranchSelect();
    initRepoIssueTitleEdit();
    initRepoIssueWipToggle();
    initRepoIssueComments();

    initRepoDiffConversationNav();
    initRepoIssueReferenceIssue();

    initRepoIssueCommentDelete();
    initRepoIssueDependencyDelete();
    initRepoIssueCodeCommentCancel();
    initRepoPullRequestUpdate();
    initCompReactionSelector($(document));

    initRepoPullRequestMergeForm();
    initRepoPullRequestCommitStatus();
  }

  // Pull request
  const $repoComparePull = $('.repository.compare.pull');
  if ($repoComparePull.length > 0) {
    // show pull request form
    $repoComparePull.find('button.show-form').on('click', function (e) {
      e.preventDefault();
      hideElem($(this).parent());

      const $form = $repoComparePull.find('.pullrequest-form');
      showElem($form);
    });
  }

  initUnicodeEscapeButton();
}

const filters = {
  A(el) {
    if (el.classList.contains('mention') || el.classList.contains('ref-issue')) {
      return el.textContent;
    }
    return el;
  },
  PRE(el) {
    const firstChild = el.children[0];
    if (firstChild && el.classList.contains('code-block')) {
      // Get the language of the codeblock.
      const language = firstChild.className.match(/language-(\S+)/);
      // Remove trailing newlines.
      const text = el.textContent.replace(/\n+$/, '');
      el.textContent = `\`\`\`${language[1]}\n${text}\n\`\`\`\n\n`;
    }
    return el;
  },
  SPAN(el) {
    const emojiAlias = el.getAttribute('data-alias');
    if (emojiAlias && el.classList.contains('emoji')) {
      return `:${emojiAlias}:`;
    }
    if (el.classList.contains('katex')) {
      const texCode = el.querySelector('annotation[encoding="application/x-tex"]').textContent;
      if (el.parentElement.classList.contains('katex-display')) {
        el.textContent = `\\[${texCode}\\]\n\n`;
      } else {
        el.textContent = `\\(${texCode}\\)\n\n`;
      }
    }
    return el;
  },
  IMG(el, context) {
    const src = el.getAttribute('src');
    if (src?.startsWith(context)) {
      el.src = src.slice(context.length);
    }
    return el;
  },
};

function hasContent(node) {
  return node.nodeName === 'IMG' || node.firstChild !== null;
}

// This code matches that of what is done by @github/quote-selection
function preprocessFragment(context) {
  return function(fragment) {
    const nodeIterator = document.createNodeIterator(fragment, NodeFilter.SHOW_ELEMENT, {
      acceptNode(node) {
        if (node.nodeName in filters && hasContent(node)) {
          return NodeFilter.FILTER_ACCEPT;
        }

        return NodeFilter.FILTER_SKIP;
      },
    });
    const results = [];
    let node = nodeIterator.nextNode();

    while (node) {
      if (node instanceof HTMLElement) {
        results.push(node);
      }
      node = nodeIterator.nextNode();
    }

  // process deepest matches first
    results.reverse();

    for (const el of results) {
      el.replaceWith(filters[el.nodeName](el, context));
    }
  };
}

function initRepoIssueCommentEdit() {
  // Edit issue or comment content
  $(document).on('click', '.edit-content', onEditContent);

  // Quote reply
  $(document).on('click', '.quote-reply', async (event) => {
    event.preventDefault();
    const quote = new MarkdownQuote('', preprocessFragment(event.target.getAttribute('data-context')));

    let editorTextArea;
    if (event.target.classList.contains('quote-reply-diff')) {
      // Temporarily store the range so it doesn't get lost (likely caused by async code).
      const currentRange = quote.range;

      const replyButton = event.target.closest('.comment-code-cloud').querySelector('button.comment-form-reply');
      editorTextArea = (await handleReply($(replyButton))).textarea;

      quote.range = currentRange;
    } else {
      editorTextArea = document.querySelector('#comment-form .combo-markdown-editor textarea');
    }

    // Select the whole comment body if there's no selection.
    if (quote.range.collapsed) {
      quote.select(document.querySelector(`#${event.target.getAttribute('data-target')}`));
    }

    // If the selection is in the comment body, then insert the quote.
    if (quote.closest(`#${event.target.getAttribute('data-target')}`)) {
      // Chromium quirk: Temporarily store the range so it doesn't get lost, caused by appending text in another element.
      const currentRange = quote.range;

      editorTextArea.value += `@${event.target.getAttribute('data-author')} wrote in ${toAbsoluteUrl(event.target.getAttribute('data-reference-url'))}:`;

      quote.range = currentRange;
      quote.insert(editorTextArea);
    }
  });
}

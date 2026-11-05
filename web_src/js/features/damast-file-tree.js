// DAMAST: file tree sidebar of the code view (MAST-17).
// The tree lives outside the content column; navigating through it fetches the target page and lets
// htmx swap only `.ui.container` (scripts in it run, hx-* attributes like the last-commit loader are
// processed). History is handled here instead of with hx-push-url, because htmx's history cache
// snapshots and restores the whole body, which would replace the mounted Vue tree with dead HTML.
import $ from 'jquery';
import htmx from 'htmx.org';
import {createApp} from 'vue';
import {GET} from '../modules/fetch.js';
import {svg} from '../svg.js';
import {initDisabledInputs} from './common-global.js';
import {initRepoBranchTagSelector} from './repo-branch-tag-selector.js';
import {initRepoArchiveLinks, initRepoCloneLink} from './repo-common.js';
import {initCopyContent} from './copycontent.js';
import {initMarkupContent} from '../markup/content.js';
import {initMarkupAnchors} from '../markup/anchors.js';
import {initCommitStatuses, initRepoEllipsisButton} from './repo-commit.js';
import {initContextPopups} from './contextpopup.js';
import {initRepoTopicBar} from './repo-home.js';
import {initRepoCodeView} from './repo-code.ts';

const contentSelector = '.page-content.repository > .ui.container';
const prefKey = 'damast-file-tree';
const mobileQuery = '(max-width: 767.98px)';

let nav, tree, codeViewReady, shownPath;

function setPref(value) {
  try {
    localStorage.setItem(prefKey, value);
  } catch {}
}

function isMobile() {
  return window.matchMedia(mobileQuery).matches;
}

// tree path of a /src/ URL of the current ref, or null for anything else
export function treePathFromUrl(pathname, repoLink, refSubUrl) {
  const prefix = decodeURIComponent(`${repoLink}/src/${refSubUrl}`);
  const decoded = decodeURIComponent(pathname);
  if (decoded === prefix) return '';
  if (!decoded.startsWith(`${prefix}/`)) return null;
  return decoded.slice(prefix.length + 1).replace(/\/$/, '');
}

async function mountTree() {
  if (tree) return;
  const {default: DamastFileTree} = await import(/* webpackChunkName: "damast-file-tree" */'../components/DamastFileTree.vue');
  nav.innerHTML = '';
  tree = createApp(DamastFileTree, {
    repoLink: nav.getAttribute('data-repo-link'),
    refSubUrl: nav.getAttribute('data-ref-sub-url'),
    initialPath: nav.getAttribute('data-tree-path'),
    // data-text-loading-tree -> texts.loadingTree
    texts: Object.fromEntries(Object.entries(nav.dataset).filter(([k]) => k.startsWith('text')).map(([k, v]) => [k[4].toLowerCase() + k.slice(5), v])),
    navigate: (url) => navigate(url, true),
  }).mount(nav);
}

function setVisible(visible, persist) {
  nav.classList.toggle('is-visible', visible);
  if (persist) setPref(visible ? 'shown' : 'hidden');
  if (visible) mountTree();
  updateToggle();
}

function updateToggle() {
  const btn = document.querySelector('.damast-file-tree-toggle');
  if (!btn) return;
  const visible = nav.classList.contains('is-visible');
  btn.setAttribute('aria-expanded', String(visible));
  const label = visible ? nav.getAttribute('data-text-hide') : nav.getAttribute('data-text-show');
  btn.setAttribute('data-tooltip-content', label);
  btn.setAttribute('aria-label', label);
  btn.innerHTML = svg(visible ? 'octicon-sidebar-collapse' : 'octicon-sidebar-expand');
}

// the button sits next to the branch dropdown, which is part of the swapped content column
function addToggle() {
  const row = document.querySelector('.repo-button-row > .button-sequence');
  if (!row || row.querySelector('.damast-file-tree-toggle')) return;
  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'ui compact basic icon button damast-file-tree-toggle';
  btn.setAttribute('aria-controls', 'damast-file-tree');
  btn.addEventListener('click', () => setVisible(!nav.classList.contains('is-visible'), !isMobile()));
  row.prepend(btn);
  updateToggle();
}

// re-run the page initialisers that bind to elements of the content column
async function initSwappedContent(container) {
  initDisabledInputs(container);
  const $dropdowns = $(container).find('.ui.dropdown:not(.custom)');
  $dropdowns.dropdown();
  $dropdowns.filter('.jump').dropdown({action: 'hide'});
  initRepoCloneLink();
  initRepoArchiveLinks();
  initCopyContent();
  initMarkupContent();
  initMarkupAnchors();
  initRepoEllipsisButton();
  initCommitStatuses();
  initContextPopups();
  initRepoTopicBar();
  // applying the #L hash makes repo-code.ts push the same URL again, which would add a history entry
  const pushState = window.history.pushState;
  window.history.pushState = window.history.replaceState;
  // the page load already registered the fold and permalink click listeners of repo-code.ts, which come
  // after its line click listener: let only that first one through
  const addEventListener = document.addEventListener;
  let clickListeners = 0;
  document.addEventListener = function (type, ...args) {
    if (type === 'click' && ++clickListeners > 1) return;
    addEventListener.call(this, type, ...args);
  };
  try {
    if (!codeViewReady && container.querySelector('.code-view .lines-num')) {
      codeViewReady = true;
      initRepoCodeView(); // registers its line listeners once and applies the #L hash
    } else if (window.location.hash) {
      window.dispatchEvent(new Event('hashchange'));
    }
  } finally {
    window.history.pushState = pushState;
    document.addEventListener = addEventListener;
  }
  addToggle();
  // the branch dropdown pushes its data from an inline module script, which runs asynchronously
  const selectors = container.querySelectorAll('.js-branch-tag-selector').length;
  for (let i = 0; i < 50 && (window.config.pageData.branchDropdownDataList?.length ?? 0) < selectors; i++) {
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  await initRepoBranchTagSelector();
}

async function navigate(url, push) {
  let pushed = false;
  try {
    const resp = await GET(url);
    const doc = new DOMParser().parseFromString(await resp.text(), 'text/html');
    const fresh = doc.querySelector(contentSelector);
    const current = document.querySelector(contentSelector);
    if (!resp.ok || !fresh || !current || !doc.getElementById('damast-file-tree')) throw new Error('not a code view page');
    const finalUrl = resp.redirected ? resp.url : url;
    if (push) {
      window.history.replaceState({...window.history.state, damastScrollY: window.scrollY}, '');
      window.history.pushState({damastTree: true}, '', finalUrl);
      pushed = true;
    }
    shownPath = window.location.pathname;
    const path = treePathFromUrl(shownPath, nav.getAttribute('data-repo-link'), nav.getAttribute('data-ref-sub-url'));
    if (path !== null) {
      nav.setAttribute('data-tree-path', path);
      if (!push) tree?.setCurrent(path);
    }
    window.config.pageData.branchDropdownDataList = [];
    // unmounted apps release their document listeners, which would otherwise keep the old column alive
    for (const el of current.querySelectorAll('.js-branch-tag-selector')) el.__vue_app__?.unmount();
    htmx.swap(current, fresh.outerHTML, {swapStyle: 'outerHTML', swapDelay: 0, settleDelay: 0}, {title: doc.title});
    await initSwappedContent(document.querySelector(contentSelector));
    const status = document.getElementById('damast-file-tree-status');
    if (status) status.textContent = doc.title; // screen readers hear that the content changed
    if (push) {
      window.scrollTo(0, 0);
      if (isMobile()) setVisible(false, false);
    } else {
      window.scrollTo(0, window.history.state?.damastScrollY ?? 0);
    }
    return true;
  } catch (err) {
    console.error('damast file tree: falling back to a full page load', err);
    if (push && !pushed) window.location.assign(url); else window.location.replace(url);
    return false;
  }
}

export function initDamastFileTree() {
  nav = document.getElementById('damast-file-tree');
  if (!nav) return;
  codeViewReady = Boolean(document.querySelector('.code-view .lines-num'));
  shownPath = window.location.pathname;
  addToggle();
  if (nav.classList.contains('is-visible')) mountTree();

  window.addEventListener('popstate', () => {
    // hash-only entries (line selection) keep the path and need no swap
    if (window.location.pathname === shownPath) return;
    const path = treePathFromUrl(window.location.pathname, nav.getAttribute('data-repo-link'), nav.getAttribute('data-ref-sub-url'));
    if (path === null) {
      window.location.reload();
      return;
    }
    navigate(window.location.href, false);
  });
}

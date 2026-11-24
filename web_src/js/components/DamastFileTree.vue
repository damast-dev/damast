<script>
// DAMAST: file tree sidebar of the code view (MAST-17). Styled after DiffFileTree.vue.
// A flat list of rows with aria-level keeps the WAI-ARIA tree keyboard model simple.
import {GET} from '../modules/fetch.js';
import iconFile from '../../../public/assets/img/svg/octicon-file.svg';
import iconDir from '../../../public/assets/img/svg/octicon-file-directory-fill.svg';
import iconDirOpen from '../../../public/assets/img/svg/octicon-file-directory-open-fill.svg';
import iconSubmodule from '../../../public/assets/img/svg/octicon-file-submodule.svg';
import iconSymlink from '../../../public/assets/img/svg/octicon-file-symlink-file.svg';
import iconChevronRight from '../../../public/assets/img/svg/octicon-chevron-right.svg';
import iconChevronDown from '../../../public/assets/img/svg/octicon-chevron-down.svg';

const maxRestoredDirs = 30;

export function escapePath(path) {
  return path.split('/').map(encodeURIComponent).join('/');
}

export function parentDirs(path) {
  const parts = path.split('/');
  return parts.slice(0, -1).map((_, i) => parts.slice(0, i + 1).join('/'));
}

export default {
  props: {
    repoLink: {type: String, required: true},
    refSubUrl: {type: String, required: true},
    initialPath: {type: String, required: true},
    // navigate(url) swaps the content column; resolves false when it fell back to a full page load
    navigate: {type: Function, required: true},
    texts: {type: Object, required: true}, // translated strings from the template (data-text-*)
  },
  data() {
    return {
      current: this.initialPath,
      dirs: {}, // dir path -> {entries, more}
      expanded: new Set(),
      loading: new Set(),
      focusPath: this.initialPath,
      error: false,
    };
  },
  computed: {
    stateKey() {
      return `damast-file-tree:${this.repoLink}:${this.refSubUrl}`;
    },
    rows() {
      const rows = [];
      const walk = (dirPath, level) => {
        const dir = this.dirs[dirPath];
        if (!dir) return;
        for (const [i, entry] of dir.entries.entries()) {
          rows.push({key: entry.path, entry, level, pos: i + 1, size: dir.entries.length});
          if (entry.type !== 'dir' || !this.expanded.has(entry.path)) continue;
          if (this.dirs[entry.path]) {
            walk(entry.path, level + 1);
          } else {
            rows.push({key: `${entry.path}/:loading`, level: level + 1, loading: true});
          }
        }
        if (dir.more) rows.push({key: `${dirPath}/:more`, level, more: dir.more, dirPath});
      };
      walk('', 1);
      return rows;
    },
    focusIndex() {
      const i = this.rows.findIndex((r) => r.entry?.path === this.focusPath);
      return i === -1 ? this.rows.findIndex((r) => r.entry) : i;
    },
  },
  async mounted() {
    let saved = null;
    try {
      saved = JSON.parse(sessionStorage.getItem(this.stateKey));
    } catch {}
    try {
      const data = await this.fetchTree(this.current, true);
      for (const dir of data.dirs) this.dirs[dir.path] = dir;
      for (const p of [...parentDirs(this.current), this.current]) {
        if (this.dirs[p] && p !== '') this.expanded.add(p);
      }
    } catch {
      this.error = true;
      return;
    }
    // phase 1 state: folders the user had open before a full page load
    const restore = (saved?.expanded ?? []).filter((p) => !this.expanded.has(p)).slice(0, maxRestoredDirs);
    await Promise.all(restore.map((p) => this.expand(p)));
    await this.$nextTick();
    if (saved && typeof saved.scrollTop === 'number') {
      this.$el.parentElement.scrollTop = saved.scrollTop;
    }
    this.$el.querySelector('.selected')?.scrollIntoView({block: 'nearest'});
    window.addEventListener('pagehide', () => this.saveState());
  },
  methods: {
    srcUrl(path) {
      return `${this.repoLink}/src/${this.refSubUrl}${path ? `/${escapePath(path)}` : ''}`;
    },
    async fetchTree(path, ancestors) {
      const url = `${this.repoLink}/tree-view/${this.refSubUrl}${path ? `/${escapePath(path)}` : ''}${ancestors ? '?ancestors=1' : ''}`;
      const resp = await GET(url);
      if (!resp.ok) throw new Error(`tree-view: ${resp.status}`);
      return resp.json();
    },
    async expand(path) {
      this.expanded.add(path);
      if (this.dirs[path] || this.loading.has(path)) return;
      this.loading.add(path);
      try {
        const data = await this.fetchTree(path, false);
        this.dirs[path] = data.dirs[0];
      } catch {
        this.expanded.delete(path); // it may be gone on this ref; keep the tree usable
      } finally {
        this.loading.delete(path);
      }
    },
    toggle(path) {
      if (this.expanded.has(path)) {
        this.expanded.delete(path);
      } else {
        this.expand(path);
      }
      this.saveState();
    },
    saveState() {
      try {
        sessionStorage.setItem(this.stateKey, JSON.stringify({
          expanded: Array.from(this.expanded),
          scrollTop: this.$el.parentElement?.scrollTop ?? 0,
        }));
      } catch {}
    },
    // called by the page when the content changed without the tree (back/forward)
    setCurrent(path) {
      this.current = path;
      this.focusPath = path;
      for (const p of parentDirs(path)) this.expand(p);
      this.$nextTick(() => this.$el.querySelector('.selected')?.scrollIntoView({block: 'nearest'}));
    },
    onLinkClick(e, entry) {
      if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return; // new tab etc.
      if (entry.type === 'submodule') return; // leaves the repository
      e.preventDefault();
      this.open(entry);
    },
    open(entry) {
      if (entry.type === 'submodule') {
        if (entry.url) window.location.href = entry.url;
        return;
      }
      if (entry.type === 'dir') this.expand(entry.path);
      this.current = entry.path;
      this.focusPath = entry.path;
      this.saveState();
      this.navigate(this.srcUrl(entry.path));
    },
    indent(level, base) {
      return {paddingInlineStart: `${(level - 1) * 16 + base}px`};
    },
    chevron(path) {
      return this.expanded.has(path) ? iconChevronDown : iconChevronRight;
    },
    icon(entry) {
      if (entry.type === 'dir') return this.expanded.has(entry.path) ? iconDirOpen : iconDir;
      if (entry.type === 'submodule') return iconSubmodule;
      if (entry.type === 'symlink') return iconSymlink;
      return iconFile;
    },
    focusRow(index) {
      const row = this.rows[index];
      if (!row?.entry) return;
      this.focusPath = row.entry.path;
      this.$nextTick(() => this.$el.querySelectorAll('[role="treeitem"]')[index]?.focus());
    },
    onKeydown(e) {
      const i = this.focusIndex;
      const row = this.rows[i];
      if (!row?.entry) return;
      const entry = row.entry;
      const isDir = entry.type === 'dir';
      const step = (dir) => {
        for (let j = i + dir; j >= 0 && j < this.rows.length; j += dir) {
          if (this.rows[j].entry) return this.focusRow(j);
        }
      };
      switch (e.key) {
        case 'ArrowDown': step(1); break;
        case 'ArrowUp': step(-1); break;
        case 'Home': this.focusRow(this.rows.findIndex((r) => r.entry)); break;
        case 'End': this.focusRow(this.rows.findLastIndex((r) => r.entry)); break;
        case 'ArrowRight':
          if (isDir && !this.expanded.has(entry.path)) this.toggle(entry.path);
          else if (isDir) step(1);
          break;
        case 'ArrowLeft':
          if (isDir && this.expanded.has(entry.path)) {
            this.toggle(entry.path);
          } else {
            const parent = parentDirs(entry.path).at(-1);
            if (parent !== undefined) this.focusRow(this.rows.findIndex((r) => r.entry?.path === parent));
          }
          break;
        case 'Enter': this.open(entry); break;
        default: return;
      }
      e.preventDefault();
    },
  },
};
</script>
<template>
  <div class="damast-file-tree-inner">
    <!-- eslint-disable vue/no-v-html -- only static, bundled SVG icons -->
    <div class="damast-file-tree-header">
      {{ texts.files }}
    </div>
    <div v-if="error" class="damast-file-tree-message">
      {{ texts.loadError }}
    </div>
    <div v-else-if="!dirs['']" class="damast-file-tree-message">
      {{ texts.loadingTree }}
    </div>
    <ul v-else role="tree" :aria-label="texts.files" class="damast-file-tree-items" @keydown="onKeydown">
      <template v-for="(row, i) in rows" :key="row.key">
        <li
          v-if="row.entry"
          role="treeitem"
          :aria-level="row.level"
          :aria-setsize="row.size"
          :aria-posinset="row.pos"
          :aria-expanded="row.entry.type === 'dir' ? String(expanded.has(row.entry.path)) : undefined"
          :aria-selected="String(row.entry.path === current)"
          :tabindex="i === focusIndex ? 0 : -1"
          class="damast-file-tree-item"
          :class="{selected: row.entry.path === current}"
          :style="indent(row.level, 2)"
          :title="row.entry.path"
          @focus="focusPath = row.entry.path"
        >
          <button
            v-if="row.entry.type === 'dir'" type="button" tabindex="-1" class="damast-file-tree-chevron"
            :aria-label="expanded.has(row.entry.path) ? texts.collapse : texts.expand"
            @click.stop="toggle(row.entry.path)"
            v-html="chevron(row.entry.path)"
          />
          <span v-else class="damast-file-tree-chevron"/>
          <a
            v-if="row.entry.type !== 'submodule' || row.entry.url"
            tabindex="-1"
            :href="row.entry.type === 'submodule' ? row.entry.url : srcUrl(row.entry.path)"
            :aria-current="row.entry.path === current ? 'page' : undefined"
            @click="onLinkClick($event, row.entry)"
          >
            <span class="damast-file-tree-icon" :class="{'text primary': row.entry.type === 'dir'}" v-html="icon(row.entry)"/>
            <span class="gt-ellipsis">{{ row.entry.name }}</span>
          </a>
          <span v-else>
            <span class="damast-file-tree-icon" v-html="icon(row.entry)"/>
            <span class="gt-ellipsis">{{ row.entry.name }}</span>
          </span>
        </li>
        <li v-else-if="row.loading" role="none" class="damast-file-tree-item damast-file-tree-note" :style="indent(row.level, 24)">
          {{ texts.loading }}
        </li>
        <li v-else role="none" class="damast-file-tree-item damast-file-tree-note" :style="indent(row.level, 24)">
          <a :href="srcUrl(row.dirPath)">{{ texts.more.replace('%s', row.more) }}</a>
        </li>
      </template>
    </ul>
  </div>
</template>
<style scoped>
.damast-file-tree-header {
  font-weight: var(--font-weight-semibold);
  padding: 12px 6px 8px;
}

.damast-file-tree-message {
  padding: 8px 6px;
  color: var(--color-text-light-2);
}

.damast-file-tree-items {
  display: flex;
  flex-direction: column;
  gap: 1px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.damast-file-tree-item {
  display: flex;
  align-items: center;
  gap: 2px;
  padding: 3px 6px;
  border-radius: 4px;
  min-width: 0;
}

.damast-file-tree-item:hover {
  background: var(--color-hover);
}

.damast-file-tree-item:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: -2px;
}

.damast-file-tree-item.selected {
  background: var(--color-active);
  box-shadow: inset 2px 0 0 var(--color-primary);
}

.damast-file-tree-item > a,
.damast-file-tree-item > span:last-child {
  display: flex;
  align-items: center;
  gap: 0.25em;
  flex: 1;
  min-width: 0;
  color: var(--color-text);
  text-decoration: none;
}

.damast-file-tree-chevron {
  display: inline-flex;
  flex: 0 0 18px;
  justify-content: center;
  padding: 0;
  border: none;
  background: none;
  color: var(--color-text-light-2);
  cursor: pointer;
}

.damast-file-tree-icon {
  display: inline-flex;
  flex: 0 0 auto;
}

.damast-file-tree-note {
  color: var(--color-text-light-2);
}

.damast-file-tree-note a {
  color: var(--color-text-light-2);
}
</style>

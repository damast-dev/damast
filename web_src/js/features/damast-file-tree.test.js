import {treePathFromUrl} from './damast-file-tree.js';

// both need browser globals the test DOM lacks (XPathEvaluator, a global jQuery)
vi.mock('htmx.org', () => ({default: {}}));
vi.mock('./common-global.js', () => ({initDisabledInputs() {}}));

test('treePathFromUrl', () => {
  expect(treePathFromUrl('/o/r/src/branch/main', '/o/r', 'branch/main')).toEqual('');
  expect(treePathFromUrl('/o/r/src/branch/main/a%20b/c%23d.go', '/o/r', 'branch/main')).toEqual('a b/c#d.go');
  expect(treePathFromUrl('/o/r/src/branch/feat/x/dir/', '/o/r', 'branch/feat/x')).toEqual('dir');
  expect(treePathFromUrl('/o/r/src/branch/mainline/x', '/o/r', 'branch/main')).toBeNull();
  expect(treePathFromUrl('/o/r/issues', '/o/r', 'branch/main')).toBeNull();
  // branch names may hold characters the server escapes and decodeURI keeps escaped
  expect(treePathFromUrl('/o/r/src/branch/fix%231/x', '/o/r', 'branch/fix%231')).toEqual('x');
  expect(treePathFromUrl('/o/r/src/branch/a%3Fb%26c/x', '/o/r', 'branch/a%3Fb%26c')).toEqual('x');
});

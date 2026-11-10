// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// DAMAST: file tree sidebar of the code view (MAST-17).

package repo

import (
	"net/http"
	"path"
	"strings"

	"forgejo.org/modules/base"
	"forgejo.org/modules/web"
	"forgejo.org/services/context"
)

// damastTreeViewMaxEntries caps one directory listing; the client links the rest to the directory page.
const damastTreeViewMaxEntries = 1000

type damastTreeEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"`          // dir, file, submodule or symlink
	URL  string `json:"url,omitempty"` // submodules only: the resolved upstream URL, empty if unknown
}

type damastTreeDir struct {
	Path    string            `json:"path"`
	Entries []damastTreeEntry `json:"entries"`
	More    int               `json:"more"` // entries left out because of damastTreeViewMaxEntries
}

// DamastTreeViewRoutes registers /tree-view/{branch,tag,commit}/* below the repo route group.
func DamastTreeViewRoutes(m *web.Route) func() {
	return func() {
		m.Get("/branch/*", context.RepoRefByType(context.RepoRefBranch), DamastTreeView)
		m.Get("/tag/*", context.RepoRefByType(context.RepoRefTag), DamastTreeView)
		m.Get("/commit/*", context.RepoRefByType(context.RepoRefCommit), DamastTreeView)
	}
}

// DamastTreeView returns one directory level as JSON: {"dirs": [{path, entries, more}]}.
// With ?ancestors=1 it returns the root and every directory on the way to the requested
// path (the path itself included when it is a directory), so the client can expand the
// tree to the current entry in a single request. The path may then also be a file.
func DamastTreeView(ctx *context.Context) {
	treePath := ctx.Repo.TreePath
	entry, err := ctx.Repo.Commit.GetTreeEntryByPath(treePath)
	if err != nil {
		HandleGitError(ctx, "GetTreeEntryByPath", err)
		return
	}

	var dirPaths []string
	if ctx.FormBool("ancestors") {
		dir := treePath
		if !entry.IsDir() {
			dir = path.Dir(treePath)
		}
		dirPaths = append(dirPaths, "")
		if dir != "" && dir != "." {
			parts := strings.Split(dir, "/")
			for i := range parts {
				dirPaths = append(dirPaths, strings.Join(parts[:i+1], "/"))
			}
		}
	} else {
		if !entry.IsDir() {
			ctx.NotFound("DamastTreeView", nil)
			return
		}
		dirPaths = []string{treePath}
	}

	dirs := make([]damastTreeDir, 0, len(dirPaths))
	for _, dirPath := range dirPaths {
		dir, err := damastListTreeDir(ctx, dirPath)
		if err != nil {
			HandleGitError(ctx, "damastListTreeDir", err)
			return
		}
		dirs = append(dirs, dir)
	}
	if ctx.Repo.IsViewCommit && ctx.Repo.RefName == ctx.Repo.Commit.ID.String() {
		// a full commit ID never changes its tree; private, because access to the repository can
		ctx.Resp.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	}
	ctx.JSON(http.StatusOK, map[string]any{"dirs": dirs})
}

func damastListTreeDir(ctx *context.Context, dirPath string) (damastTreeDir, error) {
	tree, err := ctx.Repo.Commit.SubTree(dirPath)
	if err != nil {
		return damastTreeDir{}, err
	}
	entries, err := tree.ListEntries()
	if err != nil {
		return damastTreeDir{}, err
	}
	entries.CustomSort(base.NaturalSortLess) // same order as the code view: directories first

	dir := damastTreeDir{Path: dirPath, Entries: make([]damastTreeEntry, 0, min(len(entries), damastTreeViewMaxEntries))}
	if len(entries) > damastTreeViewMaxEntries {
		dir.More = len(entries) - damastTreeViewMaxEntries
		entries = entries[:damastTreeViewMaxEntries]
	}
	for _, e := range entries {
		item := damastTreeEntry{Name: e.Name(), Path: path.Join(dirPath, e.Name()), Type: "file"}
		switch {
		case e.IsDir():
			item.Type = "dir"
		case e.IsLink():
			item.Type = "symlink"
		case e.IsSubmodule():
			item.Type = "submodule"
			sm, err := ctx.Repo.Commit.GetSubmodule(item.Path, e)
			if err != nil {
				return damastTreeDir{}, err
			}
			item.URL = sm.ResolveUpstreamURL(ctx.Repo.Repository.HTMLURL())
		}
		dir.Entries = append(dir.Entries, item)
	}
	return dir, nil
}

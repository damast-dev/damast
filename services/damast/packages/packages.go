// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package packages binds packages to repositories: auto-linking on push and write access inherited from the repo.
//
// Flow: the registry middleware grants *provisional* write (Grant) when owner-level access is not enough but the
// doer may write to some repository of the owner. Every package create/upload path then calls OnWrite inside its
// transaction, where the package is known, and OnWrite rejects the request unless the doer may write to the
// package's repository. Without a Grant, OnWrite only auto-links new packages (plain Forgejo permissions apply).
package packages

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	actions_model "forgejo.org/models/actions"
	"forgejo.org/models/damast"
	"forgejo.org/models/db"
	packages_model "forgejo.org/models/packages"
	"forgejo.org/models/perm"
	access_model "forgejo.org/models/perm/access"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/log"
	container_module "forgejo.org/modules/packages/container"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/util"
)

type grantKey struct{}

// GrantKey is the request context key holding a *Grant.
var GrantKey = grantKey{}

// Grant is provisional write access; OnWrite decides per package.
type Grant struct {
	Doer *user_model.User
	Task *actions_model.ActionTask // set when authenticated with an Actions token

	denied bool
}

// Denied reports whether OnWrite rejected this request.
func (g *Grant) Denied() bool { return g.denied }

func (g *Grant) deny() error {
	g.denied = true
	return util.ErrPermissionDenied
}

// NewGrant returns a Grant if doer may write to at least one repository of owner, or nil.
func NewGrant(ctx context.Context, owner, doer *user_model.User, taskID int64) (*Grant, error) {
	if owner == nil || doer == nil || doer.IsGhost() {
		return nil, nil
	}
	if taskID > 0 {
		task, err := actions_model.GetTaskByID(ctx, taskID)
		if err != nil {
			return nil, err
		}
		repo, err := repo_model.GetRepositoryByID(ctx, task.RepoID)
		if err != nil {
			return nil, err
		}
		// fork PRs never get package write, like GitHub
		if task.IsForkPullRequest || repo.OwnerID != owner.ID {
			return nil, nil
		}
		return &Grant{Doer: doer, Task: task}, nil
	}
	has, err := db.GetEngine(ctx).Table("access").
		Join("INNER", "repository", "repository.id = access.repo_id").
		Where("access.user_id = ? AND repository.owner_id = ? AND access.mode >= ?", doer.ID, owner.ID, perm.AccessModeWrite).
		Exist()
	if err != nil || !has {
		return nil, err
	}
	return &Grant{Doer: doer}, nil
}

func grantFrom(ctx context.Context) *Grant {
	g, _ := ctx.Value(GrantKey).(*Grant)
	return g
}

func canWrite(ctx context.Context, g *Grant, doer *user_model.User, repo *repo_model.Repository) (bool, error) {
	var p access_model.Permission
	var err error
	if g != nil && g.Task != nil {
		p, err = access_model.GetActionRepoPermission(ctx, repo, g.Task)
	} else {
		p, err = access_model.GetUserRepoPermission(ctx, repo, doer)
	}
	if err != nil {
		return false, err
	}
	return p.CanWrite(unit.TypePackages), nil
}

// OnWrite is called by every package create/upload path after the package row exists, inside its transaction.
// created reports whether the package was just inserted; metadata is the registry-specific metadata (may be nil).
func OnWrite(ctx context.Context, p *packages_model.Package, created bool, doer *user_model.User, metadata any) error {
	g := grantFrom(ctx)
	if g != nil {
		doer = g.Doer
	}

	if !created {
		if g == nil {
			return nil // owner-level access, Forgejo behaviour
		}
		if p.RepoID == 0 {
			return g.deny()
		}
		inherit, err := damast.InheritsRepoAccess(ctx, p.ID)
		if err != nil {
			return err
		}
		if !inherit {
			return g.deny()
		}
		repo, err := repo_model.GetRepositoryByID(ctx, p.RepoID)
		if err != nil {
			return err
		}
		ok, err := canWrite(ctx, g, doer, repo)
		if err != nil {
			return err
		}
		if !ok {
			return g.deny()
		}
		return nil
	}

	if g == nil && p.Type == packages_model.TypeContainer {
		// Forgejo auto-links containers itself (annotation, label, image name) once the manifest arrives
		return damast.SetInheritRepoAccess(ctx, p.ID, true)
	}

	repo, err := targetRepo(ctx, p, g, doer, metadata)
	if err != nil {
		return err
	}
	if repo == nil {
		if g != nil {
			return g.deny() // provisional access needs a repository the doer may write to
		}
		return damast.SetInheritRepoAccess(ctx, p.ID, true)
	}
	if err := packages_model.SetRepositoryLink(ctx, p.ID, repo.ID); err != nil {
		return err
	}
	p.RepoID = repo.ID
	if p.Type == packages_model.TypeContainer {
		// already linked: skip Forgejo's own container auto-linking
		if err := packages_model.DeletePropertyByName(ctx, packages_model.PropertyTypePackage, p.ID, container_module.PropertyRepositoryAutolinkingPending); err != nil {
			return err
		}
	}
	log.Info("Damast: package %d linked to repository %s", p.ID, repo.FullName())
	return damast.SetInheritRepoAccess(ctx, p.ID, true)
}

// OnWriteVersion is OnWrite for paths that add files to an existing version.
func OnWriteVersion(ctx context.Context, pv *packages_model.PackageVersion, doer *user_model.User) error {
	if grantFrom(ctx) == nil {
		return nil
	}
	p, err := packages_model.GetPackageByID(ctx, pv.PackageID)
	if err != nil {
		return err
	}
	return OnWrite(ctx, p, false, doer, nil)
}

// targetRepo picks the repository a new package belongs to: the Actions run's repo, a repo URL from the
// metadata, or a repo named like the package. Only repos of the package owner the doer may write to qualify.
func targetRepo(ctx context.Context, p *packages_model.Package, g *Grant, doer *user_model.User, metadata any) (*repo_model.Repository, error) {
	var candidates []func() (*repo_model.Repository, error)
	if g != nil && g.Task != nil {
		candidates = append(candidates, func() (*repo_model.Repository, error) { return repo_model.GetRepositoryByID(ctx, g.Task.RepoID) })
	}
	for _, name := range repoNamesFromMetadata(ctx, metadata, p.OwnerID) {
		candidates = append(candidates, byName(ctx, p.OwnerID, name))
	}
	names := repoNamesFromPackageName(p.LowerName)
	if p.Type == packages_model.TypeMaven {
		names = append(names, mavenArtifactCandidates(p.LowerName)...)
	}
	for _, name := range names {
		candidates = append(candidates, byName(ctx, p.OwnerID, name))
	}

	for _, get := range candidates {
		repo, err := get()
		if err != nil {
			if repo_model.IsErrRepoNotExist(err) {
				continue
			}
			return nil, err
		}
		if repo.OwnerID != p.OwnerID {
			continue
		}
		ok, err := canWrite(ctx, g, doer, repo)
		if err != nil {
			return nil, err
		}
		if ok {
			return repo, nil
		}
	}
	return nil, nil
}

func byName(ctx context.Context, ownerID int64, name string) func() (*repo_model.Repository, error) {
	return func() (*repo_model.Repository, error) { return repo_model.GetRepositoryByName(ctx, ownerID, name) }
}

// repoNamesFromPackageName: "foo" -> foo, "foo/bar" (container) -> foo, "@scope/foo" (npm) -> foo.
func repoNamesFromPackageName(name string) []string {
	parts := strings.Split(strings.TrimPrefix(name, "@"), "/")
	out := []string{parts[0]}
	if strings.HasPrefix(name, "@") && len(parts) > 1 {
		out = []string{parts[1]}
	}
	return out
}

// mavenArtifactCandidates: maven names packages "groupId:artifactId".
func mavenArtifactCandidates(name string) []string {
	if _, artifact, ok := strings.Cut(name, ":"); ok {
		return []string{artifact}
	}
	return nil
}

// repoNamesFromMetadata finds URLs of this instance in any string field of the metadata
// (npm repository, cargo repository, pypi project_urls, conda dev_url, OCI labels, ...).
func repoNamesFromMetadata(ctx context.Context, metadata any, ownerID int64) []string {
	if metadata == nil {
		return nil
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return nil
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	owner, err := user_model.GetUserByID(ctx, ownerID)
	if err != nil {
		return nil
	}
	app, err := url.Parse(setting.AppURL)
	if err != nil {
		return nil
	}

	var out []string
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			for _, x := range t {
				walk(x)
			}
		case []any:
			for _, x := range t {
				walk(x)
			}
		case string:
			if name := repoNameFromURL(t, app, owner.LowerName); name != "" {
				out = append(out, name)
			}
		}
	}
	walk(v)
	return out
}

// repoNameFromURL returns the repo name if u points to owner/<repo> on this instance.
func repoNameFromURL(s string, app *url.URL, owner string) string {
	s = strings.TrimPrefix(strings.TrimSpace(s), "git+")
	u, err := url.Parse(s)
	if err != nil || !strings.EqualFold(u.Host, app.Host) {
		return ""
	}
	path := strings.TrimPrefix(strings.ToLower(u.Path), strings.ToLower(strings.TrimSuffix(app.Path, "/")))
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[0] != owner {
		return ""
	}
	return strings.TrimSuffix(parts[1], ".git")
}

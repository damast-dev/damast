// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package packages

import (
	"net/http"
	"strings"

	"forgejo.org/models/perm"
	"forgejo.org/modules/setting"
	"forgejo.org/services/context"
	damast_packages "forgejo.org/services/damast/packages"
)

// Write routes that are not uploads; provisional access never applies to them.
var damastNonUploadPaths = []string{"/unyank", "/yank", "/-rev/", "/dist-tags/", "/delete"}

// Registries whose read endpoints all work per package (descriptor or file stream), so CheckRead covers
// them. Registries with owner-wide indexes (debian, rpm, alpine, arch, alt, cargo, composer, helm,
// rubygems, ...) would leak other packages and keep owner-level read.
var damastReadTypes = map[string]bool{"generic": true, "npm": true, "maven": true, "pypi": true}

// Read routes that list several packages; provisional read never applies to them. Per-package lists
// (npm dist-tags, container tags/list) are fine: hidden packages look missing there (GetPackageByName,
// GetVersionsByPackageName).
var damastListPaths = []string{"/_catalog", "/-/v1/search"}

// damastUploadGet reports GET steps of multi-step uploads (pub: ask for an upload URL, finalize). They get
// provisional write like the upload itself; finalize checks the package like any write to an existing one.
// Matched exactly, so no read route (pub package "versions", maven paths) ever gets write.
func damastUploadGet(path string) bool {
	parts := strings.SplitN(strings.TrimPrefix(path, "/api/packages/"), "/", 3)
	if !strings.HasPrefix(path, "/api/packages/") || len(parts) < 3 || parts[1] != "pub" {
		return false
	}
	return parts[2] == "api/packages/versions/new" || strings.HasPrefix(parts[2], "api/packages/versions/new/finalize/")
}

// damastProvisionalAccess grants provisional access when owner-level access is not enough but the doer
// has access to a repository of the owner: write for uploads, read for downloads/metadata of the
// registries above. services/damast/packages (OnWrite, CheckRead) then checks the concrete package.
// DELETE and other non-upload writes keep Forgejo's owner-level rules.
func damastProvisionalAccess(ctx *context.Context) {
	if ctx.Package == nil || ctx.Package.Owner == nil {
		return
	}
	mode := damastRequestMode(ctx.Req)
	// site admins pass reqPackageAccess anyway; a Grant would only narrow them to their repositories
	if mode == perm.AccessModeNone || ctx.Package.AccessMode >= mode || ctx.IsUserSiteAdmin() {
		return
	}
	_, taskID := ctx.Authentication.ActionsTaskID().Get()
	g, err := damast_packages.NewGrant(ctx, mode, ctx.Package.Owner, ctx.Doer, taskID)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "damastProvisionalAccess", err.Error())
		return
	}
	if g != nil {
		g.Reducer = ctx.Authentication.Reducer() // public-only tokens must not reach private repositories
		ctx.Package.AccessMode = mode
		ctx.Base.AppendContextValue(damast_packages.GrantKey, g)
		ctx.Resp = &damastDenyResponse{ResponseWriter: ctx.Resp, grant: g}
	}
}

// damastDenyResponse rewrites the status when Damast rejected the request (handlers compare errors by
// equality and answer unknown ones with 500). Denied reads become 404, so a package the reader may not
// see looks like one that does not exist. Denied uploads become 403: a taken name can't be hidden there.
type damastDenyResponse struct {
	context.ResponseWriter
	grant *damast_packages.Grant
}

func (r *damastDenyResponse) WriteHeader(status int) {
	if r.grant.Denied() && status >= http.StatusBadRequest {
		if r.grant.Mode == perm.AccessModeRead {
			status = http.StatusNotFound
		} else if status == http.StatusInternalServerError {
			status = http.StatusForbidden
		}
	}
	r.ResponseWriter.WriteHeader(status)
}

// damastRequestMode returns the access a request may get provisionally, or AccessModeNone.
func damastRequestMode(req *http.Request) perm.AccessMode {
	path := strings.ToLower(strings.TrimPrefix(req.URL.Path, setting.AppSubURL))
	switch req.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		for _, p := range damastNonUploadPaths {
			if strings.Contains(path, p) {
				return perm.AccessModeNone
			}
		}
		return perm.AccessModeWrite
	case http.MethodGet, http.MethodHead:
		if damastUploadGet(path) {
			return perm.AccessModeWrite
		}
		for _, p := range damastListPaths {
			if strings.Contains(path, p) {
				return perm.AccessModeNone
			}
		}
		if strings.HasPrefix(path, "/v2/") {
			return perm.AccessModeRead // container
		}
		// /api/packages/{owner}/{type}/...
		if parts := strings.Split(strings.TrimPrefix(path, "/api/packages/"), "/"); strings.HasPrefix(path, "/api/packages/") && len(parts) > 1 && damastReadTypes[parts[1]] {
			return perm.AccessModeRead
		}
	}
	return perm.AccessModeNone
}

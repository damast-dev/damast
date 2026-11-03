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

// Registries whose upload handlers change existing package data before the central OnWrite check.
// shortcut: no inherited write for these until their handlers are hooked too.
var damastNoInheritTypes = map[string]bool{"maven": true, "conan": true}

// Write routes that are not uploads; provisional access never applies to them.
var damastNonUploadPaths = []string{"/unyank", "/yank", "/-rev/", "/dist-tags/", "/delete"}

// damastProvisionalWrite grants provisional write for upload requests when owner-level access is not
// enough but the doer may write to a repository of the owner. services/damast/packages.OnWrite then
// checks the concrete package. DELETE and other non-upload writes keep Forgejo's owner-level rules.
func damastProvisionalWrite(ctx *context.Context) {
	if ctx.Package == nil || ctx.Package.Owner == nil || ctx.Package.AccessMode >= perm.AccessModeWrite || !damastIsUpload(ctx.Req) {
		return
	}
	_, taskID := ctx.Authentication.ActionsTaskID().Get()
	g, err := damast_packages.NewGrant(ctx, ctx.Package.Owner, ctx.Doer, taskID)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "damastProvisionalWrite", err.Error())
		return
	}
	if g != nil {
		ctx.Package.AccessMode = perm.AccessModeWrite
		ctx.Base.AppendContextValue(damast_packages.GrantKey, g)
	}
}

func damastIsUpload(req *http.Request) bool {
	switch req.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
	default:
		return false
	}
	path := strings.ToLower(strings.TrimPrefix(req.URL.Path, setting.AppSubURL))
	for _, p := range damastNonUploadPaths {
		if strings.Contains(path, p) {
			return false
		}
	}
	// /api/packages/{owner}/{type}/...; container uses /v2/...
	if parts := strings.Split(strings.TrimPrefix(path, "/api/packages/"), "/"); strings.HasPrefix(path, "/api/packages/") && len(parts) > 1 && damastNoInheritTypes[parts[1]] {
		return false
	}
	return true
}

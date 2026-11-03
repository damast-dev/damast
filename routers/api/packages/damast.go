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
		ctx.Resp = &damastDenyResponse{ResponseWriter: ctx.Resp, grant: g}
	}
}

// damastDenyResponse turns the 500 that registry handlers send for unknown errors into a 403
// when OnWrite rejected the request (handlers compare errors by equality, so they can't map it).
type damastDenyResponse struct {
	context.ResponseWriter
	grant *damast_packages.Grant
}

func (r *damastDenyResponse) WriteHeader(status int) {
	if status == http.StatusInternalServerError && r.grant.Denied() {
		status = http.StatusForbidden
	}
	r.ResponseWriter.WriteHeader(status)
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
	return true
}

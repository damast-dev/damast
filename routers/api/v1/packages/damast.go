// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package packages

import (
	"errors"
	"net/http"
	"slices"

	"forgejo.org/models/damast"
	packages_model "forgejo.org/models/packages"
	"forgejo.org/modules/json"
	"forgejo.org/services/context"
)

// DamastOwnerPackageSettings are an owner's Damast package settings.
// swagger:model
type DamastOwnerPackageSettings struct {
	// who may create new package names: owner-level package writers only (default), or also repository
	// writers and Actions runs, in container, npm, Maven, NuGet, RubyGems and generic registries
	// enum: owners,repo_writers
	NewPackages string `json:"new_packages"`
}

// DamastPackageSettings are a package's Damast settings.
// swagger:model
type DamastPackageSettings struct {
	// readers and writers of the linked repository may download and publish new versions of the package
	InheritRepoAccess bool `json:"inherit_repo_access"`
}

const (
	damastNewPackagesOwners      = "owners"
	damastNewPackagesRepoWriters = "repo_writers"
)

// DamastGetOwnerSettings gets an owner's Damast package settings
func DamastGetOwnerSettings(ctx *context.APIContext) {
	// swagger:operation GET /damast/packages/{owner}/settings package damastGetOwnerPackageSettings
	// ---
	// summary: Gets an owner's Damast package settings (Damast only)
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the packages
	//   type: string
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/DamastOwnerPackageSettings"
	//   "403":
	//     "$ref": "#/responses/forbidden"
	//   "404":
	//     "$ref": "#/responses/notFound"

	damastServeOwnerSettings(ctx)
}

// DamastEditOwnerSettings changes an owner's Damast package settings
func DamastEditOwnerSettings(ctx *context.APIContext) {
	// swagger:operation PATCH /damast/packages/{owner}/settings package damastEditOwnerPackageSettings
	// ---
	// summary: Changes an owner's Damast package settings (Damast only)
	// consumes:
	// - application/json
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the packages
	//   type: string
	//   required: true
	// - name: body
	//   in: body
	//   schema:
	//     "$ref": "#/definitions/DamastOwnerPackageSettings"
	// responses:
	//   "200":
	//     "$ref": "#/responses/DamastOwnerPackageSettings"
	//   "403":
	//     "$ref": "#/responses/forbidden"
	//   "404":
	//     "$ref": "#/responses/notFound"
	//   "422":
	//     "$ref": "#/responses/validationError"

	var form DamastOwnerPackageSettings
	if err := json.NewDecoder(ctx.Req.Body).Decode(&form); err != nil {
		ctx.Error(http.StatusUnprocessableEntity, "DamastEditOwnerSettings", err)
		return
	}
	if form.NewPackages != damastNewPackagesOwners && form.NewPackages != damastNewPackagesRepoWriters {
		ctx.Error(http.StatusUnprocessableEntity, "DamastEditOwnerSettings", `new_packages must be "owners" or "repo_writers"`)
		return
	}
	if err := damast.SetRepoWritersMayCreatePackages(ctx, ctx.Package().Owner.ID, form.NewPackages == damastNewPackagesRepoWriters); err != nil {
		ctx.InternalServerError(err)
		return
	}
	damastServeOwnerSettings(ctx)
}

func damastServeOwnerSettings(ctx *context.APIContext) {
	allow, err := damast.RepoWritersMayCreatePackages(ctx, ctx.Package().Owner.ID)
	if err != nil {
		ctx.InternalServerError(err)
		return
	}
	s := DamastOwnerPackageSettings{NewPackages: damastNewPackagesOwners}
	if allow {
		s.NewPackages = damastNewPackagesRepoWriters
	}
	ctx.JSON(http.StatusOK, s)
}

// DamastGetPackageSettings gets a package's Damast settings
func DamastGetPackageSettings(ctx *context.APIContext) {
	// swagger:operation GET /damast/packages/{owner}/{type}/{name}/settings package damastGetPackageSettings
	// ---
	// summary: Gets a package's Damast settings (Damast only)
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the package
	//   type: string
	//   required: true
	// - name: type
	//   in: path
	//   description: type of the package
	//   type: string
	//   required: true
	// - name: name
	//   in: path
	//   description: name of the package
	//   type: string
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/DamastPackageSettings"
	//   "404":
	//     "$ref": "#/responses/notFound"

	p := damastPackage(ctx)
	if p == nil {
		return
	}
	damastServePackageSettings(ctx, p)
}

// DamastEditPackageSettings changes a package's Damast settings
func DamastEditPackageSettings(ctx *context.APIContext) {
	// swagger:operation PATCH /damast/packages/{owner}/{type}/{name}/settings package damastEditPackageSettings
	// ---
	// summary: Changes a package's Damast settings (Damast only)
	// consumes:
	// - application/json
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the package
	//   type: string
	//   required: true
	// - name: type
	//   in: path
	//   description: type of the package
	//   type: string
	//   required: true
	// - name: name
	//   in: path
	//   description: name of the package
	//   type: string
	//   required: true
	// - name: body
	//   in: body
	//   schema:
	//     "$ref": "#/definitions/DamastPackageSettings"
	// responses:
	//   "200":
	//     "$ref": "#/responses/DamastPackageSettings"
	//   "403":
	//     "$ref": "#/responses/forbidden"
	//   "404":
	//     "$ref": "#/responses/notFound"
	//   "422":
	//     "$ref": "#/responses/validationError"

	p := damastPackage(ctx)
	if p == nil {
		return
	}
	var form DamastPackageSettings
	if err := json.NewDecoder(ctx.Req.Body).Decode(&form); err != nil {
		ctx.Error(http.StatusUnprocessableEntity, "DamastEditPackageSettings", err)
		return
	}
	if err := damast.SetInheritRepoAccess(ctx, p.ID, form.InheritRepoAccess); err != nil {
		ctx.InternalServerError(err)
		return
	}
	damastServePackageSettings(ctx, p)
}

func damastPackage(ctx *context.APIContext) *packages_model.Package {
	t := packages_model.Type(ctx.Params("type"))
	if !slices.Contains(packages_model.TypeList, t) {
		ctx.NotFound()
		return nil
	}
	p, err := packages_model.GetPackageByName(ctx, ctx.Package().Owner.ID, t, ctx.Params("name"))
	if errors.Is(err, packages_model.ErrPackageNotExist) {
		ctx.NotFound()
		return nil
	}
	if err != nil {
		ctx.InternalServerError(err)
		return nil
	}
	return p
}

func damastServePackageSettings(ctx *context.APIContext, p *packages_model.Package) {
	inherit, err := damast.InheritsRepoAccess(ctx, p.ID)
	if err != nil {
		ctx.InternalServerError(err)
		return
	}
	ctx.JSON(http.StatusOK, DamastPackageSettings{InheritRepoAccess: inherit})
}

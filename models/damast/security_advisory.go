// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package damast

import (
	"context"
	"crypto/rand"
	"math/big"
	"time"

	"forgejo.org/models/db"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/timeutil"
	"forgejo.org/modules/util"

	"xorm.io/builder"
)

// Advisory states, the same as GitHub's repository security advisories.
const (
	AdvisoryTriage    = "triage"    // private vulnerability report, not yet accepted
	AdvisoryDraft     = "draft"     // accepted or created by a maintainer, not public
	AdvisoryPublished = "published" // public to everyone who can read the repository
	AdvisoryClosed    = "closed"    // rejected or abandoned, not public
	AdvisoryWithdrawn = "withdrawn" // published, then retracted; stays public and marked as withdrawn
)

// AffectedProduct is one package an advisory applies to.
type AffectedProduct struct {
	Ecosystem       string `json:"ecosystem"`        // GitHub ecosystem key: npm, pip, maven, ...
	Package         string `json:"package"`          // package name in that ecosystem
	VulnerableRange string `json:"vulnerable_range"` // e.g. ">= 1.0, < 1.4.2"
	Patched         string `json:"patched"`          // first fixed version, optional
}

// Credit names a user who helped with the advisory.
type Credit struct {
	UserID int64  `json:"user_id"`
	Type   string `json:"type"` // OSV/GitHub credit type in lower case: finder, reporter, ...
}

// SecurityAdvisory is a repository security advisory or a private vulnerability report (state triage).
// Unpublished rows must only ever be shown through services/damast/security.AccessLevel.
type SecurityAdvisory struct {
	ID            int64              `xorm:"pk autoincr"`
	RepoID        int64              `xorm:"INDEX NOT NULL"`
	AdvisoryID    string             `xorm:"'advisory_id' UNIQUE NOT NULL"` // public id, see NewAdvisoryID
	State         string             `xorm:"INDEX NOT NULL"`
	CreatorID     int64              `xorm:"NOT NULL"`
	ReporterID    int64              `xorm:"INDEX NOT NULL DEFAULT 0"` // set for private reports only
	Title         string             `xorm:"NOT NULL"`
	Description   string             `xorm:"LONGTEXT"`
	Severity      string             `xorm:"NOT NULL"` // low, moderate, high, critical
	CVSSVector    string             `xorm:"'cvss_vector'"`
	CVEID         string             `xorm:"'cve_id'"`
	CWEIDs        []string           `xorm:"'cwe_ids' TEXT JSON"`
	Affected      []AffectedProduct  `xorm:"TEXT JSON"`
	Credits       []Credit           `xorm:"TEXT JSON"`
	CreatedUnix   timeutil.TimeStamp `xorm:"created"`
	UpdatedUnix   timeutil.TimeStamp `xorm:"updated"` // OSV "modified"
	PublishedUnix timeutil.TimeStamp
	ClosedUnix    timeutil.TimeStamp
	WithdrawnUnix timeutil.TimeStamp // OSV "withdrawn"
}

func (SecurityAdvisory) TableName() string { return "damast_security_advisory" }

// AdvisoryCollaborator gives a user access to one unpublished advisory.
type AdvisoryCollaborator struct {
	ID          int64              `xorm:"pk autoincr"`
	AdvisoryID  int64              `xorm:"UNIQUE(s) NOT NULL"`
	UserID      int64              `xorm:"UNIQUE(s) INDEX NOT NULL"`
	CreatedUnix timeutil.TimeStamp `xorm:"created"`
}

func (AdvisoryCollaborator) TableName() string { return "damast_security_advisory_collaborator" }

// AdvisoryComment is a comment in the private discussion of an advisory. It stays private after publishing.
type AdvisoryComment struct {
	ID          int64              `xorm:"pk autoincr"`
	AdvisoryID  int64              `xorm:"INDEX NOT NULL"`
	PosterID    int64              `xorm:"NOT NULL"`
	Content     string             `xorm:"LONGTEXT"`
	CreatedUnix timeutil.TimeStamp `xorm:"created"`
	UpdatedUnix timeutil.TimeStamp `xorm:"updated"`
}

func (AdvisoryComment) TableName() string { return "damast_security_advisory_comment" }

// SecuritySetting holds the security settings of a repository. No row means defaults (reporting off).
type SecuritySetting struct {
	RepoID           int64 `xorm:"pk"`
	PrivateReporting bool  `xorm:"NOT NULL DEFAULT false"`
}

func (SecuritySetting) TableName() string { return "damast_security_setting" }

func init() {
	db.RegisterModel(new(SecurityAdvisory))
	db.RegisterModel(new(AdvisoryCollaborator))
	db.RegisterModel(new(AdvisoryComment))
	db.RegisterModel(new(SecuritySetting))
}

// advisoryAlphabet is the alphabet of GHSA ids: no vowels, no look-alikes.
const advisoryAlphabet = "23456789cfghjmpqrvwx"

// NewAdvisoryID returns a random id like DAMAST-7h2c-9xq4-m5vw. Random instead of owner/repo/number because an
// OSV id must never change (repositories get renamed and transferred) and must not reveal how many private
// advisories a repository has.
func NewAdvisoryID() string {
	b := []byte("DAMAST-xxxx-xxxx-xxxx")
	for i := range b {
		if b[i] == 'x' {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(advisoryAlphabet))))
			if err != nil {
				panic(err)
			}
			b[i] = advisoryAlphabet[n.Int64()]
		}
	}
	return string(b)
}

// IsPublished reports whether everyone who can read the repository may see the advisory: it is published, or
// was published and then withdrawn (a withdrawn advisory stays visible so scanners learn about the withdrawal).
func (a *SecurityAdvisory) IsPublished() bool {
	return a.State == AdvisoryPublished || a.State == AdvisoryWithdrawn
}

// CreateAdvisory inserts a new advisory with a fresh public id.
func CreateAdvisory(ctx context.Context, a *SecurityAdvisory) error {
	a.AdvisoryID = NewAdvisoryID()
	return db.Insert(ctx, a)
}

// GetAdvisory returns the advisory with the public id in the repository, or util.ErrNotExist.
func GetAdvisory(ctx context.Context, repoID int64, advisoryID string) (*SecurityAdvisory, error) {
	a := &SecurityAdvisory{}
	has, err := db.GetEngine(ctx).Where("repo_id = ? AND advisory_id = ?", repoID, advisoryID).Get(a)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, util.ErrNotExist
	}
	return a, nil
}

// RepoAdvisories returns all advisories of a repository, newest first, regardless of state.
// Callers must filter them with services/damast/security.AccessLevel.
func RepoAdvisories(ctx context.Context, repoID int64) ([]*SecurityAdvisory, error) {
	var list []*SecurityAdvisory
	return list, db.GetEngine(ctx).Where("repo_id = ?", repoID).OrderBy("id DESC").Find(&list)
}

// UpdateAdvisory saves the given columns (all if none are given).
func UpdateAdvisory(ctx context.Context, a *SecurityAdvisory, cols ...string) error {
	sess := db.GetEngine(ctx).ID(a.ID)
	if len(cols) > 0 {
		sess = sess.Cols(cols...)
	} else {
		sess = sess.AllCols()
	}
	_, err := sess.Update(a)
	return err
}

// CountReports counts the reports of a user, optionally only in one repository and state and since a time.
func CountReports(ctx context.Context, reporterID, repoID int64, state string, since time.Time) (int64, error) {
	cond := builder.Eq{"reporter_id": reporterID}
	if repoID > 0 {
		cond["repo_id"] = repoID
	}
	if state != "" {
		cond["state"] = state
	}
	sess := db.GetEngine(ctx).Where(cond)
	if !since.IsZero() {
		sess = sess.And("created_unix >= ?", since.Unix())
	}
	return sess.Count(new(SecurityAdvisory))
}

// IsAdvisoryCollaborator reports whether the user was added to the advisory.
func IsAdvisoryCollaborator(ctx context.Context, advisoryID, userID int64) (bool, error) {
	return db.GetEngine(ctx).Where("advisory_id = ? AND user_id = ?", advisoryID, userID).Exist(new(AdvisoryCollaborator))
}

// AdvisoryCollaboratorIDs returns the user ids of the collaborators of an advisory.
func AdvisoryCollaboratorIDs(ctx context.Context, advisoryID int64) ([]int64, error) {
	var ids []int64
	return ids, db.GetEngine(ctx).Table("damast_security_advisory_collaborator").Where("advisory_id = ?", advisoryID).OrderBy("id").Cols("user_id").Find(&ids)
}

// AddAdvisoryCollaborator adds a user, doing nothing if they are already a collaborator.
func AddAdvisoryCollaborator(ctx context.Context, advisoryID, userID int64) error {
	has, err := IsAdvisoryCollaborator(ctx, advisoryID, userID)
	if err != nil || has {
		return err
	}
	return db.Insert(ctx, &AdvisoryCollaborator{AdvisoryID: advisoryID, UserID: userID})
}

// RemoveAdvisoryCollaborator removes a user from an advisory.
func RemoveAdvisoryCollaborator(ctx context.Context, advisoryID, userID int64) error {
	_, err := db.GetEngine(ctx).Where("advisory_id = ? AND user_id = ?", advisoryID, userID).Delete(new(AdvisoryCollaborator))
	return err
}

// CreateAdvisoryComment inserts a comment.
func CreateAdvisoryComment(ctx context.Context, c *AdvisoryComment) error {
	return db.Insert(ctx, c)
}

// GetAdvisoryComment returns a comment of the advisory, or util.ErrNotExist.
func GetAdvisoryComment(ctx context.Context, advisoryID, commentID int64) (*AdvisoryComment, error) {
	c := &AdvisoryComment{}
	has, err := db.GetEngine(ctx).Where("advisory_id = ? AND id = ?", advisoryID, commentID).Get(c)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, util.ErrNotExist
	}
	return c, nil
}

// UpdateAdvisoryComment saves the content of a comment.
func UpdateAdvisoryComment(ctx context.Context, c *AdvisoryComment) error {
	_, err := db.GetEngine(ctx).ID(c.ID).Cols("content").Update(c)
	return err
}

// DeleteAdvisoryComment deletes a comment.
func DeleteAdvisoryComment(ctx context.Context, c *AdvisoryComment) error {
	_, err := db.GetEngine(ctx).ID(c.ID).Delete(new(AdvisoryComment))
	return err
}

// AdvisoryComments returns the comments of an advisory, oldest first.
func AdvisoryComments(ctx context.Context, advisoryID int64) ([]*AdvisoryComment, error) {
	var list []*AdvisoryComment
	return list, db.GetEngine(ctx).Where("advisory_id = ?", advisoryID).OrderBy("id").Find(&list)
}

// PrivateReportingEnabled reports whether the repository accepts private vulnerability reports.
func PrivateReportingEnabled(ctx context.Context, repoID int64) (bool, error) {
	s := &SecuritySetting{}
	has, err := db.GetEngine(ctx).ID(repoID).Get(s)
	return has && s.PrivateReporting, err
}

// SetPrivateReporting turns private vulnerability reporting on or off.
func SetPrivateReporting(ctx context.Context, repoID int64, enabled bool) error {
	e := db.GetEngine(ctx)
	has, err := e.ID(repoID).Exist(&SecuritySetting{})
	if err != nil {
		return err
	}
	if has {
		_, err = e.ID(repoID).Cols("private_reporting").Update(&SecuritySetting{PrivateReporting: enabled})
		return err
	}
	_, err = e.Insert(&SecuritySetting{RepoID: repoID, PrivateReporting: enabled})
	return err
}

// DeleteRepoSecurityData removes all advisories, reports, comments and settings of a repository.
func DeleteRepoSecurityData(ctx context.Context, repoID int64) error {
	e := db.GetEngine(ctx)
	ids := builder.Select("id").From("damast_security_advisory").Where(builder.Eq{"repo_id": repoID})
	if _, err := e.Where(builder.In("advisory_id", ids)).Delete(new(AdvisoryComment)); err != nil {
		return err
	}
	if _, err := e.Where(builder.In("advisory_id", ids)).Delete(new(AdvisoryCollaborator)); err != nil {
		return err
	}
	if _, err := e.Where("repo_id = ?", repoID).Delete(new(SecurityAdvisory)); err != nil {
		return err
	}
	_, err := e.ID(repoID).Delete(new(SecuritySetting))
	return err
}

// DeleteUserSecurityData runs when a user is deleted: their reports and comments stay and show the ghost
// user, their advisory collaborations go. Rewriting the ids (instead of keeping dangling ones) means no
// later account can ever inherit access to the reports.
func DeleteUserSecurityData(ctx context.Context, userID int64) error {
	e := db.GetEngine(ctx)
	if _, err := e.Where("user_id = ?", userID).Delete(new(AdvisoryCollaborator)); err != nil {
		return err
	}
	if _, err := e.Table("damast_security_advisory").Where("reporter_id = ?", userID).Update(map[string]any{"reporter_id": user_model.GhostUserID}); err != nil {
		return err
	}
	if _, err := e.Table("damast_security_advisory").Where("creator_id = ?", userID).Update(map[string]any{"creator_id": user_model.GhostUserID}); err != nil {
		return err
	}
	_, err := e.Table("damast_security_advisory_comment").Where("poster_id = ?", userID).Update(map[string]any{"poster_id": user_model.GhostUserID})
	return err
}

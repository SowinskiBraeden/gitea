// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v28

import (
	"context"

	"gitea.dev/modelmigration/base"
	"gitea.dev/modules/timeutil"
)

func AddUserContributionTable(_ context.Context, x base.EngineMigration) error {
	type UserContribution struct {
		ID               int64              `xorm:"pk autoincr"`
		UserID           int64              `xorm:"INDEX NOT NULL"`
		RepoID           int64              `xorm:"INDEX UNIQUE(repo_commit_type) NOT NULL"`
		CommitSHA        string             `xorm:"VARCHAR(64) UNIQUE(repo_commit_type) NOT NULL"`
		AuthorEmail      string             `xorm:"VARCHAR(254)"`
		ContributionType string             `xorm:"VARCHAR(16) UNIQUE(repo_commit_type) NOT NULL"`
		ContributionUnix timeutil.TimeStamp `xorm:"INDEX NOT NULL"`
		CreatedUnix      timeutil.TimeStamp `xorm:"created"`
		UpdatedUnix      timeutil.TimeStamp `xorm:"updated"`
	}
	return x.Sync(new(UserContribution))
}

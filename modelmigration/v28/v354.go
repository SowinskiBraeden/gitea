// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v28

import (
	"context"

	"gitea.dev/modelmigration/base"
	"gitea.dev/modules/timeutil"
)

func AddUserLanguageStatTable(_ context.Context, x base.EngineMigration) error {
	type UserLanguageStat struct {
		ID          int64              `xorm:"pk autoincr"`
		UserID      int64              `xorm:"INDEX UNIQUE(user_repo_year_language_visibility) NOT NULL"`
		RepoID      int64              `xorm:"INDEX UNIQUE(user_repo_year_language_visibility) NOT NULL"`
		Year        int                `xorm:"INDEX UNIQUE(user_repo_year_language_visibility) NOT NULL"`
		Language    string             `xorm:"VARCHAR(64) UNIQUE(user_repo_year_language_visibility) NOT NULL"`
		Visibility  string             `xorm:"VARCHAR(16) UNIQUE(user_repo_year_language_visibility) NOT NULL"`
		Additions   int64              `xorm:"NOT NULL DEFAULT 0"`
		Deletions   int64              `xorm:"NOT NULL DEFAULT 0"`
		Commits     int64              `xorm:"NOT NULL DEFAULT 0"`
		UpdatedUnix timeutil.TimeStamp `xorm:"updated"`
	}
	return x.Sync(new(UserLanguageStat))
}

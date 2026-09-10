// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package activities

import (
	"context"
	"strconv"
	"time"

	"gitea.dev/models/db"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/timeutil"

	"xorm.io/builder"
)

// UserContributionType is the source kind of a reconstructed contribution.
type UserContributionType string

const UserContributionTypeCommit UserContributionType = "commit"

// UserContribution represents a Git-history contribution reconstructed from repository contents.
type UserContribution struct {
	ID               int64                `xorm:"pk autoincr"`
	UserID           int64                `xorm:"INDEX NOT NULL"`
	RepoID           int64                `xorm:"INDEX UNIQUE(repo_commit_type) NOT NULL"`
	CommitSHA        string               `xorm:"VARCHAR(64) UNIQUE(repo_commit_type) NOT NULL"`
	AuthorEmail      string               `xorm:"VARCHAR(254)"`
	ContributionType UserContributionType `xorm:"VARCHAR(16) UNIQUE(repo_commit_type) NOT NULL"`
	ContributionUnix timeutil.TimeStamp   `xorm:"INDEX NOT NULL"`
	CreatedUnix      timeutil.TimeStamp   `xorm:"created"`
	UpdatedUnix      timeutil.TimeStamp   `xorm:"updated"`
}

func init() {
	db.RegisterModel(new(UserContribution))
}

// ReplaceUserContributionsForRepo replaces a repository's reconstructed contribution rows.
func ReplaceUserContributionsForRepo(ctx context.Context, repoID int64, contributions []*UserContribution) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		e := db.GetEngine(ctx)
		if _, err := e.Where("repo_id = ?", repoID).Delete(&UserContribution{}); err != nil {
			return err
		}
		for start, batchSize := 0, db.MaxBatchInsertSize(new(UserContribution)); start < len(contributions); start += batchSize {
			end := min(start+batchSize, len(contributions))
			if _, err := e.Insert(contributions[start:end]); err != nil {
				return err
			}
		}
		return nil
	})
}

func DeleteUserContributionsByRepoID(ctx context.Context, repoID int64) error {
	_, err := db.GetEngine(ctx).Where("repo_id = ?", repoID).Delete(&UserContribution{})
	return err
}

// ReplaceUserContributionsForUser replaces a user's reconstructed contribution rows.
func ReplaceUserContributionsForUser(ctx context.Context, userID int64, contributions []*UserContribution) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		e := db.GetEngine(ctx)
		if _, err := e.Where("user_id = ?", userID).Delete(&UserContribution{}); err != nil {
			return err
		}
		for start, batchSize := 0, db.MaxBatchInsertSize(new(UserContribution)); start < len(contributions); start += batchSize {
			end := min(start+batchSize, len(contributions))
			if _, err := e.Insert(contributions[start:end]); err != nil {
				return err
			}
		}
		return nil
	})
}

func getUserContributionHeatmapData(ctx context.Context, user, doer *user_model.User, start, end time.Time) ([]*UserHeatmapData, error) {
	hdata := make([]*UserHeatmapData, 0)
	if user.IsOrganization() || !ActivityReadable(user, doer) {
		return hdata, nil
	}

	groupBy, groupByName := heatmapGroupBy("contribution_unix")
	cond := builder.Eq{"user_id": user.ID}.
		And(builder.Gte{"contribution_unix": start.Unix()}).
		And(builder.Lt{"contribution_unix": end.Unix()})
	canReadAll := doer != nil && (doer.IsAdmin || doer.ID == user.ID)
	showPrivate, err := UserShowsPrivateContributions(ctx, user)
	if err != nil {
		return nil, err
	}
	if !canReadAll && !showPrivate {
		cond = cond.And(builder.In("repo_id", repo_model.AccessibleRepoIDsQuery(doer)))
	}

	return hdata, db.GetEngine(ctx).
		Select(groupBy + " AS timestamp, count(user_id) as contributions").
		Table("user_contribution").
		Where(cond).
		GroupBy(groupByName).
		OrderBy("timestamp").
		Find(&hdata)
}

func UserShowsPrivateContributions(ctx context.Context, user *user_model.User) (bool, error) {
	if user == nil || user.ID == 0 {
		return false, nil
	}
	value, err := user_model.GetUserSetting(ctx, user.ID, user_model.SettingsKeyContributionShowPrivate, "false")
	if err != nil {
		return false, err
	}
	show, err := strconv.ParseBool(value)
	if err != nil {
		return false, nil
	}
	return show, nil
}

func heatmapGroupBy(column string) (string, string) {
	groupBy := column + " / 900 * 900"
	groupByName := "timestamp"
	switch {
	case setting.Database.Type.IsMySQL():
		groupBy = column + " DIV 900 * 900"
	case setting.Database.Type.IsMSSQL():
		groupByName = groupBy
	}
	return groupBy, groupByName
}

// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package activities

import (
	"context"
	"slices"

	"gitea.dev/models/db"
	"gitea.dev/modules/timeutil"

	"xorm.io/builder"
)

// UserLanguageStatVisibility separates public and private contribution language activity.
type UserLanguageStatVisibility string

const (
	UserLanguageStatVisibilityPublic  UserLanguageStatVisibility = "public"
	UserLanguageStatVisibilityPrivate UserLanguageStatVisibility = "private"
)

// UserLanguageStat stores per-user language totals reconstructed from accepted Git-history contributions.
type UserLanguageStat struct {
	ID          int64                      `xorm:"pk autoincr"`
	UserID      int64                      `xorm:"INDEX UNIQUE(user_repo_year_language_visibility) NOT NULL"`
	RepoID      int64                      `xorm:"INDEX UNIQUE(user_repo_year_language_visibility) NOT NULL"`
	Year        int                        `xorm:"INDEX UNIQUE(user_repo_year_language_visibility) NOT NULL"`
	Language    string                     `xorm:"VARCHAR(64) UNIQUE(user_repo_year_language_visibility) NOT NULL"`
	Visibility  UserLanguageStatVisibility `xorm:"VARCHAR(16) UNIQUE(user_repo_year_language_visibility) NOT NULL"`
	Additions   int64                      `xorm:"NOT NULL DEFAULT 0"`
	Deletions   int64                      `xorm:"NOT NULL DEFAULT 0"`
	Commits     int64                      `xorm:"NOT NULL DEFAULT 0"`
	UpdatedUnix timeutil.TimeStamp         `xorm:"updated"`
}

type UserLanguageStatSummary struct {
	Language     string
	Additions    int64
	Deletions    int64
	ChangedLines int64 `xorm:"-"`
	Commits      int64
	Percentage   float64 `xorm:"-"`
}

func init() {
	db.RegisterModel(new(UserLanguageStat))
}

func (stat *UserLanguageStat) ChangedLines() int64 {
	return stat.Additions + stat.Deletions
}

// ReplaceUserLanguageStatsForRepo replaces reconstructed language totals for one repository.
func ReplaceUserLanguageStatsForRepo(ctx context.Context, repoID int64, stats []*UserLanguageStat) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		e := db.GetEngine(ctx)
		if _, err := e.Where("repo_id = ?", repoID).Delete(&UserLanguageStat{}); err != nil {
			return err
		}
		return insertUserLanguageStats(ctx, stats)
	})
}

// ReplaceUserLanguageStatsForUser replaces reconstructed language totals for one user.
func ReplaceUserLanguageStatsForUser(ctx context.Context, userID int64, stats []*UserLanguageStat) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		e := db.GetEngine(ctx)
		if _, err := e.Where("user_id = ?", userID).Delete(&UserLanguageStat{}); err != nil {
			return err
		}
		return insertUserLanguageStats(ctx, stats)
	})
}

func insertUserLanguageStats(ctx context.Context, stats []*UserLanguageStat) error {
	e := db.GetEngine(ctx)
	for start, batchSize := 0, db.MaxBatchInsertSize(new(UserLanguageStat)); start < len(stats); start += batchSize {
		end := min(start+batchSize, len(stats))
		if _, err := e.Insert(stats[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func GetUserLanguageStats(ctx context.Context, userID int64, year int, includePrivate bool) ([]*UserLanguageStatSummary, error) {
	stats := make([]*UserLanguageStatSummary, 0)
	var cond builder.Cond = builder.Eq{"user_id": userID}
	if year > 0 {
		cond = cond.And(builder.Eq{"year": year})
	}
	if !includePrivate {
		cond = cond.And(builder.Eq{"visibility": UserLanguageStatVisibilityPublic})
	}
	if err := db.GetEngine(ctx).
		Select("language, sum(additions) AS additions, sum(deletions) AS deletions, sum(commits) AS commits").
		Table("user_language_stat").
		Where(cond).
		GroupBy("language").
		Find(&stats); err != nil {
		return nil, err
	}

	var total int64
	for _, stat := range stats {
		stat.ChangedLines = stat.Additions + stat.Deletions
		total += stat.ChangedLines
	}
	if total > 0 {
		for _, stat := range stats {
			stat.Percentage = float64(stat.ChangedLines) / float64(total) * 100
		}
	}
	slices.SortFunc(stats, func(a, b *UserLanguageStatSummary) int {
		if a.ChangedLines != b.ChangedLines {
			return int(b.ChangedLines - a.ChangedLines)
		}
		return stringsCompare(a.Language, b.Language)
	})
	return stats, nil
}

func stringsCompare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

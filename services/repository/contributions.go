// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	activities_model "gitea.dev/models/activities"
	"gitea.dev/models/db"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/git"
	"gitea.dev/modules/graceful"
	"gitea.dev/modules/log"
	"gitea.dev/modules/queue"
	"gitea.dev/modules/timeutil"

	"xorm.io/builder"
)

var historicalContributionsQueue *queue.WorkerPoolQueue[int64]

func initHistoricalContributionsQueue() error {
	historicalContributionsQueue = queue.CreateUniqueQueue(graceful.GetManager().ShutdownContext(), "historical_contributions", historicalContributionsQueueHandler)
	if historicalContributionsQueue == nil {
		return errors.New("unable to create historical_contributions queue")
	}
	go graceful.GetManager().RunWithCancel(historicalContributionsQueue)
	return nil
}

func historicalContributionsQueueHandler(items ...int64) []int64 {
	for _, repoID := range items {
		repo, err := repo_model.GetRepositoryByID(graceful.GetManager().ShutdownContext(), repoID)
		if err != nil {
			log.Error("GetRepositoryByID[%d]: %v", repoID, err)
			continue
		}
		count, err := RebuildHistoricalContributionsForRepo(graceful.GetManager().ShutdownContext(), repo)
		if err != nil {
			log.Error("RebuildHistoricalContributionsForRepo[%s]: %v", repo.FullName(), err)
		} else {
			log.Trace("Rebuilt %d historical contributions for %s", count, repo.FullName())
		}
	}
	return nil
}

func AddRepoToHistoricalContributionsQueue(repoID int64) error {
	if historicalContributionsQueue == nil {
		return nil
	}
	return historicalContributionsQueue.Push(repoID)
}

type RebuildHistoricalContributionsOptions struct {
	RepoID int64
}

type RebuildHistoricalContributionsResult struct {
	Repos         int64
	Contributions int64
}

func RebuildHistoricalContributions(ctx context.Context, opts RebuildHistoricalContributionsOptions) (*RebuildHistoricalContributionsResult, error) {
	result := &RebuildHistoricalContributionsResult{}
	if opts.RepoID != 0 {
		repo, err := repo_model.GetRepositoryByID(ctx, opts.RepoID)
		if err != nil {
			return nil, err
		}
		count, err := RebuildHistoricalContributionsForRepo(ctx, repo)
		if err != nil {
			return nil, err
		}
		result.Repos = 1
		result.Contributions = int64(count)
		return result, nil
	}

	const pageSize = repo_model.RepositoryListDefaultPageSize
	var lastID int64
	for {
		repos := make([]*repo_model.Repository, 0, pageSize)
		if err := db.GetEngine(ctx).
			Where(builder.Gt{"id": lastID}).
			And(builder.Eq{"is_empty": false, "status": repo_model.RepositoryReady}).
			Asc("id").
			Limit(pageSize).
			Find(&repos); err != nil {
			return nil, err
		}
		if len(repos) == 0 {
			return result, nil
		}
		for _, repo := range repos {
			lastID = repo.ID
			count, err := RebuildHistoricalContributionsForRepo(ctx, repo)
			if err != nil {
				log.Warn("RebuildHistoricalContributionsForRepo[%s]: %v", repo.FullName(), err)
				continue
			}
			result.Repos++
			result.Contributions += int64(count)
		}
	}
}

func RebuildHistoricalContributionsForRepo(ctx context.Context, repo *repo_model.Repository) (int, error) {
	if repo.IsEmpty || repo.DefaultBranch == "" {
		if err := activities_model.ReplaceUserContributionsForRepo(ctx, repo.ID, nil); err != nil {
			return 0, err
		}
		return 0, nil
	}

	emailUserIDs := map[string]int64{}
	contributions := make([]*activities_model.UserContribution, 0)
	revision := git.BranchPrefix + repo.DefaultBranch
	err := git.WalkCommitAuthors(ctx, repo, revision, func(author git.CommitAuthor) error {
		email := strings.ToLower(author.Email)
		if email == "" {
			return nil
		}
		userID, ok := emailUserIDs[email]
		if !ok {
			u, err := user_model.GetUserByEmail(ctx, email)
			if err != nil {
				emailUserIDs[email] = 0
				return nil
			}
			userID = u.ID
			emailUserIDs[email] = userID
		}
		if userID == 0 {
			return nil
		}
		contributions = append(contributions, &activities_model.UserContribution{
			UserID:           userID,
			RepoID:           repo.ID,
			CommitSHA:        author.SHA,
			AuthorEmail:      email,
			ContributionType: activities_model.UserContributionTypeCommit,
			ContributionUnix: timeutil.TimeStamp(author.When.Unix()),
		})
		return nil
	})
	if err != nil {
		if git.IsErrNotExist(err) {
			return 0, activities_model.ReplaceUserContributionsForRepo(ctx, repo.ID, nil)
		}
		return 0, fmt.Errorf("WalkCommitAuthors: %w", err)
	}
	if err := activities_model.ReplaceUserContributionsForRepo(ctx, repo.ID, contributions); err != nil {
		return 0, err
	}
	return len(contributions), nil
}

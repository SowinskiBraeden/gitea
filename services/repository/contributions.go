// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repository

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	activities_model "gitea.dev/models/activities"
	"gitea.dev/models/db"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/git"
	"gitea.dev/modules/git/attribute"
	"gitea.dev/modules/git/languagestats"
	"gitea.dev/modules/graceful"
	"gitea.dev/modules/log"
	"gitea.dev/modules/queue"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/timeutil"

	"xorm.io/builder"
)

var (
	historicalContributionsQueue     *queue.WorkerPoolQueue[int64]
	historicalUserContributionsQueue *queue.WorkerPoolQueue[int64]
	runningUserContributionRebuilds  sync.Map
)

func initHistoricalContributionsQueue() error {
	historicalContributionsQueue = queue.CreateUniqueQueue(graceful.GetManager().ShutdownContext(), "historical_contributions", historicalContributionsQueueHandler)
	if historicalContributionsQueue == nil {
		return errors.New("unable to create historical_contributions queue")
	}
	go graceful.GetManager().RunWithCancel(historicalContributionsQueue)

	historicalUserContributionsQueue = queue.CreateUniqueQueue(graceful.GetManager().ShutdownContext(), "historical_user_contributions", historicalUserContributionsQueueHandler)
	if historicalUserContributionsQueue == nil {
		return errors.New("unable to create historical_user_contributions queue")
	}
	go graceful.GetManager().RunWithCancel(historicalUserContributionsQueue)
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

func historicalUserContributionsQueueHandler(items ...int64) []int64 {
	for _, userID := range items {
		if _, loaded := runningUserContributionRebuilds.LoadOrStore(userID, struct{}{}); loaded {
			continue
		}
		func() {
			defer runningUserContributionRebuilds.Delete(userID)
			u, err := user_model.GetUserByID(graceful.GetManager().ShutdownContext(), userID)
			if err != nil {
				log.Error("GetUserByID[%d]: %v", userID, err)
				return
			}
			result, err := RebuildHistoricalContributions(graceful.GetManager().ShutdownContext(), RebuildHistoricalContributionsOptions{UserID: userID})
			if err != nil {
				log.Error("RebuildHistoricalContributions[%s]: %v", u.Name, err)
			} else {
				log.Trace("Rebuilt %d historical contributions for %s across %d repositories", result.Contributions, u.Name, result.Repos)
			}
		}()
	}
	return nil
}

func AddRepoToHistoricalContributionsQueue(repoID int64) error {
	if historicalContributionsQueue == nil {
		return nil
	}
	return historicalContributionsQueue.Push(repoID)
}

func AddUserToHistoricalContributionsQueue(userID int64) (bool, error) {
	if historicalUserContributionsQueue == nil {
		return false, nil
	}
	if _, loaded := runningUserContributionRebuilds.Load(userID); loaded {
		return false, nil
	}
	has, err := historicalUserContributionsQueue.Has(userID)
	if err != nil {
		return false, err
	}
	if has {
		return false, nil
	}
	return true, historicalUserContributionsQueue.Push(userID)
}

type RebuildHistoricalContributionsOptions struct {
	RepoID int64
	UserID int64
	Years  int
}

type RebuildHistoricalContributionsResult struct {
	Repos         int64
	Contributions int64
	ByYear        map[int]int64
}

type historicalContributionRepoResult struct {
	Contributions []*activities_model.UserContribution
	LanguageStats []*activities_model.UserLanguageStat
}

type historicalLanguageStatKey struct {
	UserID     int64
	RepoID     int64
	Year       int
	Language   string
	Visibility activities_model.UserLanguageStatVisibility
}

type historicalLanguageStatAccumulator struct {
	additions int64
	deletions int64
	commits   int64
}

const historicalLanguageFileSizeLimit int64 = 16 * 1024

func RebuildHistoricalContributions(ctx context.Context, opts RebuildHistoricalContributionsOptions) (*RebuildHistoricalContributionsResult, error) {
	var err error
	opts, err = normalizeHistoricalContributionOptions(ctx, opts)
	if err != nil {
		return nil, err
	}
	result := &RebuildHistoricalContributionsResult{ByYear: map[int]int64{}}
	if opts.RepoID != 0 {
		repo, err := repo_model.GetRepositoryByID(ctx, opts.RepoID)
		if err != nil {
			return nil, err
		}
		repoResult, err := collectHistoricalContributionDataForRepo(ctx, repo, opts)
		if err != nil {
			return nil, err
		}
		if opts.UserID == 0 {
			if err := activities_model.ReplaceUserContributionsForRepo(ctx, repo.ID, repoResult.Contributions); err != nil {
				return nil, err
			}
			if err := activities_model.ReplaceUserLanguageStatsForRepo(ctx, repo.ID, repoResult.LanguageStats); err != nil {
				return nil, err
			}
		} else if err := replaceUserContributionDataForRepos(ctx, opts.UserID, []int64{repo.ID}, repoResult); err != nil {
			return nil, err
		}
		result.addRepo(repoResult.Contributions)
		return result, nil
	}
	if opts.UserID != 0 {
		return rebuildHistoricalContributionsForUser(ctx, opts)
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
			repoResult, err := collectHistoricalContributionDataForRepo(ctx, repo, opts)
			if err != nil {
				log.Warn("RebuildHistoricalContributionsForRepo[%s]: %v", repo.FullName(), err)
				continue
			}
			if err := activities_model.ReplaceUserContributionsForRepo(ctx, repo.ID, repoResult.Contributions); err != nil {
				return nil, err
			}
			if err := activities_model.ReplaceUserLanguageStatsForRepo(ctx, repo.ID, repoResult.LanguageStats); err != nil {
				return nil, err
			}
			result.addRepo(repoResult.Contributions)
		}
	}
}

func RebuildHistoricalContributionsForRepo(ctx context.Context, repo *repo_model.Repository) (int, error) {
	repoResult, err := collectHistoricalContributionDataForRepo(ctx, repo, RebuildHistoricalContributionsOptions{})
	if err != nil {
		return 0, err
	}
	if err := activities_model.ReplaceUserContributionsForRepo(ctx, repo.ID, repoResult.Contributions); err != nil {
		return 0, err
	}
	if err := activities_model.ReplaceUserLanguageStatsForRepo(ctx, repo.ID, repoResult.LanguageStats); err != nil {
		return 0, err
	}
	return len(repoResult.Contributions), nil
}

func rebuildHistoricalContributionsForUser(ctx context.Context, opts RebuildHistoricalContributionsOptions) (*RebuildHistoricalContributionsResult, error) {
	result := &RebuildHistoricalContributionsResult{ByYear: map[int]int64{}}
	contributions := make([]*activities_model.UserContribution, 0)
	languageStats := make([]*activities_model.UserLanguageStat, 0)

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
			break
		}
		for _, repo := range repos {
			lastID = repo.ID
			repoResult, err := collectHistoricalContributionDataForRepo(ctx, repo, opts)
			if err != nil {
				log.Warn("RebuildHistoricalContributionsForRepo[%s]: %v", repo.FullName(), err)
				continue
			}
			result.addRepo(repoResult.Contributions)
			contributions = append(contributions, repoResult.Contributions...)
			languageStats = append(languageStats, repoResult.LanguageStats...)
		}
	}
	if err := activities_model.ReplaceUserContributionsForUser(ctx, opts.UserID, contributions); err != nil {
		return nil, err
	}
	if err := activities_model.ReplaceUserLanguageStatsForUser(ctx, opts.UserID, languageStats); err != nil {
		return nil, err
	}
	return result, nil
}

func collectHistoricalContributionDataForRepo(ctx context.Context, repo *repo_model.Repository, opts RebuildHistoricalContributionsOptions) (*historicalContributionRepoResult, error) {
	if repo.IsEmpty || repo.DefaultBranch == "" {
		return &historicalContributionRepoResult{}, nil
	}

	userEmails, err := contributionUserEmails(ctx, opts.UserID)
	if err != nil {
		return nil, err
	}
	years := maxHistoricalContributionYears(opts.Years)
	since := historicalContributionStart(years)
	emailUserIDs := make(map[string]int64, len(userEmails))
	contributions := make([]*activities_model.UserContribution, 0)
	languageStats := make(map[historicalLanguageStatKey]*historicalLanguageStatAccumulator)
	var gitRepo *git.Repository
	defer func() {
		if gitRepo != nil {
			gitRepo.Close()
		}
	}()
	revision := git.BranchPrefix + repo.DefaultBranch
	err = git.WalkCommitAuthorsSince(ctx, repo, revision, since, func(author git.CommitAuthor) error {
		if author.When.Before(since) {
			return nil
		}
		email := strings.ToLower(author.Email)
		if email == "" {
			return nil
		}
		if opts.UserID != 0 {
			if _, ok := userEmails[email]; !ok {
				return nil
			}
		}
		userID, ok := emailUserIDs[email]
		if !ok {
			if opts.UserID != 0 {
				userID = opts.UserID
			} else {
				u, err := user_model.GetUserByEmail(ctx, email)
				if err != nil {
					emailUserIDs[email] = 0
					return nil
				}
				userID = u.ID
			}
			emailUserIDs[email] = userID
		}
		if userID == 0 {
			return nil
		}
		contributionUnix := timeutil.TimeStamp(author.When.Unix())
		contributions = append(contributions, &activities_model.UserContribution{
			UserID:           userID,
			RepoID:           repo.ID,
			CommitSHA:        author.SHA,
			AuthorEmail:      email,
			ContributionType: activities_model.UserContributionTypeCommit,
			ContributionUnix: contributionUnix,
		})
		if gitRepo == nil {
			openedRepo, err := git.OpenRepository(ctx, repo)
			if err != nil {
				log.Warn("OpenRepository[%s] for historical language stats: %v", repo.FullName(), err)
				return nil
			}
			gitRepo = openedRepo
		}
		if err := collectHistoricalLanguageStatsForCommit(ctx, gitRepo, repo, userID, author.SHA, contributionUnix, languageStats); err != nil {
			log.Warn("CollectHistoricalLanguageStatsForCommit[%s:%s]: %v", repo.FullName(), author.SHA, err)
		}
		return nil
	})
	if err != nil {
		if git.IsErrNotExist(err) {
			return &historicalContributionRepoResult{}, nil
		}
		return nil, fmt.Errorf("WalkCommitAuthors: %w", err)
	}
	return &historicalContributionRepoResult{
		Contributions: contributions,
		LanguageStats: historicalLanguageStatsFromMap(languageStats),
	}, nil
}

func collectHistoricalLanguageStatsForCommit(ctx context.Context, gitRepo *git.Repository, repo *repo_model.Repository, userID int64, commitSHA string, contributionUnix timeutil.TimeStamp, languageStats map[historicalLanguageStatKey]*historicalLanguageStatAccumulator) error {
	fileStats, err := git.GetCommitFileStats(ctx, gitRepo, commitSHA)
	if err != nil {
		return err
	}
	if len(fileStats) == 0 {
		return nil
	}
	commit, err := gitRepo.GetCommit(ctx, commitSHA)
	if err != nil {
		return err
	}

	visibility := activities_model.UserLanguageStatVisibilityPublic
	if repo.IsPrivate {
		visibility = activities_model.UserLanguageStatVisibilityPrivate
	}
	year := contributionUnix.AsTime().In(setting.DefaultUILocation).Year()
	checkers := map[string]*attribute.BatchChecker{}
	defer func() {
		for _, checker := range checkers {
			_ = checker.Close()
		}
	}()

	commitLanguages := map[string]struct{}{}
	for _, fileStat := range fileStats {
		if fileStat.IsBinary || fileStat.Additions+fileStat.Deletions == 0 {
			continue
		}
		language := detectHistoricalChangedFileLanguage(ctx, gitRepo, commit, fileStat, checkers)
		if language == "" {
			continue
		}
		key := historicalLanguageStatKey{
			UserID:     userID,
			RepoID:     repo.ID,
			Year:       year,
			Language:   language,
			Visibility: visibility,
		}
		accumulator := languageStats[key]
		if accumulator == nil {
			accumulator = &historicalLanguageStatAccumulator{}
			languageStats[key] = accumulator
		}
		accumulator.additions += fileStat.Additions
		accumulator.deletions += fileStat.Deletions
		commitLanguages[language] = struct{}{}
	}
	for language := range commitLanguages {
		key := historicalLanguageStatKey{
			UserID:     userID,
			RepoID:     repo.ID,
			Year:       year,
			Language:   language,
			Visibility: visibility,
		}
		languageStats[key].commits++
	}
	return nil
}

func detectHistoricalChangedFileLanguage(ctx context.Context, gitRepo *git.Repository, commit *git.Commit, fileStat git.CommitFileStat, checkers map[string]*attribute.BatchChecker) string {
	candidates := []struct {
		commit *git.Commit
		path   string
	}{
		{commit: commit, path: fileStat.Path},
	}
	if fileStat.OldPath != "" && fileStat.OldPath != fileStat.Path {
		candidates = append(candidates, struct {
			commit *git.Commit
			path   string
		}{commit: commit, path: fileStat.OldPath})
	}
	if parent, err := commit.Parent(ctx, gitRepo, 0); err == nil {
		if fileStat.OldPath != "" {
			candidates = append(candidates, struct {
				commit *git.Commit
				path   string
			}{commit: parent, path: fileStat.OldPath})
		}
		candidates = append(candidates, struct {
			commit *git.Commit
			path   string
		}{commit: parent, path: fileStat.Path})
	}

	for _, candidate := range candidates {
		if candidate.path == "" {
			continue
		}
		content, ok := readHistoricalLanguageBlob(ctx, gitRepo, candidate.commit, candidate.path)
		if !ok {
			continue
		}
		attrs := historicalLanguageAttributes(ctx, gitRepo, candidate.commit.ID.String(), candidate.path, checkers)
		if language := languagestats.DetectFileLanguage(candidate.path, content, attrs); language != "" {
			return language
		}
	}
	return ""
}

func readHistoricalLanguageBlob(ctx context.Context, gitRepo *git.Repository, commit *git.Commit, treePath string) ([]byte, bool) {
	blob, err := commit.GetBlobByPath(ctx, gitRepo, treePath)
	if err != nil {
		return nil, false
	}
	content, err := blob.GetBlobBytes(ctx, historicalLanguageFileSizeLimit)
	if err != nil {
		return nil, false
	}
	return content, true
}

func historicalLanguageAttributes(ctx context.Context, gitRepo *git.Repository, treeish, treePath string, checkers map[string]*attribute.BatchChecker) *attribute.Attributes {
	checker := checkers[treeish]
	if checker == nil {
		var err error
		checker, err = attribute.NewBatchChecker(ctx, gitRepo, treeish, attribute.LinguistAttributes)
		if err != nil {
			return nil
		}
		checkers[treeish] = checker
	}
	attrs, err := checker.CheckPath(treePath)
	if err != nil {
		return nil
	}
	return attrs
}

func historicalLanguageStatsFromMap(languageStats map[historicalLanguageStatKey]*historicalLanguageStatAccumulator) []*activities_model.UserLanguageStat {
	stats := make([]*activities_model.UserLanguageStat, 0, len(languageStats))
	for key, accumulator := range languageStats {
		stats = append(stats, &activities_model.UserLanguageStat{
			UserID:     key.UserID,
			RepoID:     key.RepoID,
			Year:       key.Year,
			Language:   key.Language,
			Visibility: key.Visibility,
			Additions:  accumulator.additions,
			Deletions:  accumulator.deletions,
			Commits:    accumulator.commits,
		})
	}
	return stats
}

func contributionUserEmails(ctx context.Context, userID int64) (map[string]struct{}, error) {
	if userID == 0 {
		return map[string]struct{}{}, nil
	}
	emails, err := user_model.GetActivatedEmailAddresses(ctx, userID)
	if err != nil {
		return nil, err
	}
	userEmails := make(map[string]struct{}, len(emails))
	for _, email := range emails {
		userEmails[strings.ToLower(email)] = struct{}{}
	}
	return userEmails, nil
}

func replaceUserContributionDataForRepos(ctx context.Context, userID int64, repoIDs []int64, result *historicalContributionRepoResult) error {
	slices.Sort(repoIDs)
	repoIDs = slices.Compact(repoIDs)
	return db.WithTx(ctx, func(ctx context.Context) error {
		e := db.GetEngine(ctx)
		if len(repoIDs) == 0 {
			if _, err := e.Where("user_id = ?", userID).Delete(&activities_model.UserContribution{}); err != nil {
				return err
			}
			if _, err := e.Where("user_id = ?", userID).Delete(&activities_model.UserLanguageStat{}); err != nil {
				return err
			}
		} else {
			if _, err := e.Where("user_id = ?", userID).In("repo_id", repoIDs).Delete(&activities_model.UserContribution{}); err != nil {
				return err
			}
			if _, err := e.Where("user_id = ?", userID).In("repo_id", repoIDs).Delete(&activities_model.UserLanguageStat{}); err != nil {
				return err
			}
		}
		for start, batchSize := 0, db.MaxBatchInsertSize(new(activities_model.UserContribution)); start < len(result.Contributions); start += batchSize {
			end := min(start+batchSize, len(result.Contributions))
			if _, err := e.Insert(result.Contributions[start:end]); err != nil {
				return err
			}
		}
		for start, batchSize := 0, db.MaxBatchInsertSize(new(activities_model.UserLanguageStat)); start < len(result.LanguageStats); start += batchSize {
			end := min(start+batchSize, len(result.LanguageStats))
			if _, err := e.Insert(result.LanguageStats[start:end]); err != nil {
				return err
			}
		}
		return nil
	})
}

func (result *RebuildHistoricalContributionsResult) addRepo(contributions []*activities_model.UserContribution) {
	result.Repos++
	result.Contributions += int64(len(contributions))
	for _, contribution := range contributions {
		result.ByYear[contribution.ContributionUnix.AsTime().In(setting.DefaultUILocation).Year()]++
	}
}

func maxHistoricalContributionYears(years int) int {
	maxYears := max(setting.Service.UserHeatmapMaxYears, 1)
	if years <= 0 {
		return maxYears
	}
	return min(years, maxYears)
}

func historicalContributionStart(years int) time.Time {
	now := timeutil.TimeStampNow().AsTime().In(setting.DefaultUILocation)
	return time.Date(now.Year()-years+1, 1, 1, 0, 0, 0, 0, setting.DefaultUILocation)
}

func ParseContributionHistoryYears(value string) int {
	years, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return maxHistoricalContributionYears(years)
}

func normalizeHistoricalContributionOptions(ctx context.Context, opts RebuildHistoricalContributionsOptions) (RebuildHistoricalContributionsOptions, error) {
	if opts.UserID == 0 || opts.Years > 0 {
		opts.Years = maxHistoricalContributionYears(opts.Years)
		return opts, nil
	}
	user, err := user_model.GetUserByID(ctx, opts.UserID)
	if err != nil {
		return opts, err
	}
	years, err := activities_model.UserContributionHistoryYears(ctx, user)
	if err != nil {
		return opts, err
	}
	opts.Years = maxHistoricalContributionYears(years)
	return opts, nil
}

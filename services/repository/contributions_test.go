// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repository

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	activities_model "gitea.dev/models/activities"
	"gitea.dev/models/db"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/git"
	"gitea.dev/modules/git/gitcmd"
	"gitea.dev/modules/git/gitrepo"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/timeutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRebuildHistoricalContributionsForRepo(t *testing.T) {
	unittest.PrepareTestEnv(t)
	oldMaxYears := setting.Service.UserHeatmapMaxYears
	setting.Service.UserHeatmapMaxYears = 10
	defer func() { setting.Service.UserHeatmapMaxYears = oldMaxYears }()

	owner := &user_model.User{
		LowerName:      "historical-contributor",
		Name:           "historical-contributor",
		Email:          "historical@example.com",
		Passwd:         "password",
		PasswdHashAlgo: "dummy",
		Type:           user_model.UserTypeIndividual,
		IsActive:       true,
		CreatedUnix:    timeutil.TimeStamp(time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC).Unix()),
	}
	_, err := db.GetEngine(t.Context()).NoAutoTime().Insert(owner)
	require.NoError(t, err)
	_, err = user_model.InsertEmailAddress(t.Context(), &user_model.EmailAddress{
		UID:         owner.ID,
		Email:       owner.Email,
		IsActivated: true,
		IsPrimary:   true,
	})
	require.NoError(t, err)
	repo := &repo_model.Repository{
		OwnerID:          owner.ID,
		OwnerName:        owner.Name,
		LowerName:        "historical-contributions",
		Name:             "historical-contributions",
		DefaultBranch:    "master",
		IsEmpty:          false,
		Status:           repo_model.RepositoryReady,
		ObjectFormatName: git.Sha1ObjectFormat.Name(),
		IsFsckEnabled:    true,
	}
	require.NoError(t, db.Insert(t.Context(), repo))

	repoPath := gitrepo.RepoLocalPath(gitrepo.CodeRepoByName(owner.Name, repo.Name))
	require.NoError(t, git.InitRepositoryLocal(t.Context(), repoPath, true, git.Sha1ObjectFormat.Name()))
	require.NoError(t, fastImportContributionHistory(t, repoPath))
	require.NoError(t, gitcmd.NewCommand("branch").AddDynamicArguments("duplicate", "master").
		WithRepo(gitrepo.RepositoryUnmanaged(filepath.Clean(repoPath))).
		Run(t.Context()))

	count, err := RebuildHistoricalContributionsForRepo(t.Context(), repo)
	require.NoError(t, err)
	assert.Equal(t, 3, count)
	assert.Equal(t, 3, unittest.GetCount(t, &activities_model.UserContribution{RepoID: repo.ID}))

	count, err = RebuildHistoricalContributionsForRepo(t.Context(), repo)
	require.NoError(t, err)
	assert.Equal(t, 3, count)
	assert.Equal(t, 3, unittest.GetCount(t, &activities_model.UserContribution{RepoID: repo.ID}))

	heatmap2019, err := activities_model.GetUserHeatmapDataByUserAndYear(t.Context(), owner, owner, 2019)
	require.NoError(t, err)
	assert.EqualValues(t, 2, heatmapTotal(heatmap2019))

	heatmap2020, err := activities_model.GetUserHeatmapDataByUserAndYear(t.Context(), owner, owner, 2020)
	require.NoError(t, err)
	assert.EqualValues(t, 1, heatmapTotal(heatmap2020))

	unittest.AssertNotExistsBean(t, &activities_model.UserContribution{RepoID: repo.ID}, unittest.Cond("author_email = ?", "other@example.com"))
	unittest.AssertNotExistsBean(t, &activities_model.UserContribution{RepoID: repo.ID}, unittest.Cond("contribution_unix = ?", contributionUnix(2021, 1, 1)))

	result, err := RebuildHistoricalContributions(t.Context(), RebuildHistoricalContributionsOptions{RepoID: repo.ID, UserID: owner.ID, Years: 7})
	require.NoError(t, err)
	assert.EqualValues(t, 1, result.Repos)
	assert.EqualValues(t, 1, result.Contributions)
	assert.EqualValues(t, 1, result.ByYear[2020])

	result, err = RebuildHistoricalContributions(t.Context(), RebuildHistoricalContributionsOptions{RepoID: repo.ID, UserID: owner.ID, Years: 7})
	require.NoError(t, err)
	assert.EqualValues(t, 1, result.Contributions)
	assert.Equal(t, 1, unittest.GetCount(t, &activities_model.UserContribution{RepoID: repo.ID, UserID: owner.ID}))
}

func fastImportContributionHistory(t *testing.T, repoPath string) error {
	t.Helper()
	type commit struct {
		ref     string
		mark    int
		from    int
		email   string
		when    int64
		path    string
		content string
	}
	commits := []commit{
		{ref: "refs/heads/master", mark: 1, email: "historical@example.com", when: contributionUnix(2019, 1, 1), path: "one.txt", content: "one"},
		{ref: "refs/heads/master", mark: 2, from: 1, email: "other@example.com", when: contributionUnix(2019, 1, 2), path: "two.txt", content: "two"},
		{ref: "refs/heads/master", mark: 3, from: 2, email: "HISTORICAL@example.com", when: contributionUnix(2019, 1, 1) + 3600, path: "three.txt", content: "three"},
		{ref: "refs/heads/master", mark: 4, from: 3, email: "historical@example.com", when: contributionUnix(2020, 6, 1), path: "four.txt", content: "four"},
		{ref: "refs/heads/side", mark: 5, email: "historical@example.com", when: contributionUnix(2021, 1, 1), path: "side.txt", content: "side"},
	}

	var input strings.Builder
	for _, c := range commits {
		fmt.Fprintf(&input, "commit %s\nmark :%d\n", c.ref, c.mark)
		fmt.Fprintf(&input, "author Test <%s> %d +0000\n", c.email, c.when)
		fmt.Fprintf(&input, "committer Importer <importer@example.com> %d +0000\n", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix())
		message := fmt.Sprintf("commit %d", c.mark)
		fmt.Fprintf(&input, "data %d\n%s\n", len(message), message)
		if c.from != 0 {
			fmt.Fprintf(&input, "from :%d\n", c.from)
		}
		fmt.Fprintf(&input, "M 100644 inline %s\ndata %d\n%s\n", c.path, len(c.content), c.content)
	}
	input.WriteString("done\n")
	return gitcmd.NewCommand("fast-import").AddArguments("--force", "--done").
		WithRepo(gitrepo.RepositoryUnmanaged(filepath.Clean(repoPath))).
		WithStdinBytes([]byte(input.String())).
		Run(t.Context())
}

func contributionUnix(year int, month time.Month, day int) int64 {
	return time.Date(year, month, day, 12, 0, 0, 0, time.UTC).Unix()
}

func heatmapTotal(data []*activities_model.UserHeatmapData) int64 {
	var total int64
	for _, item := range data {
		total += item.Contributions
	}
	return total
}

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

func TestRebuildHistoricalContributionLanguageStats(t *testing.T) {
	unittest.PrepareTestEnv(t)
	oldMaxYears := setting.Service.UserHeatmapMaxYears
	setting.Service.UserHeatmapMaxYears = 10
	defer func() { setting.Service.UserHeatmapMaxYears = oldMaxYears }()

	owner := &user_model.User{
		LowerName:      "language-contributor",
		Name:           "language-contributor",
		Email:          "language@example.com",
		Passwd:         "password",
		PasswdHashAlgo: "dummy",
		Type:           user_model.UserTypeIndividual,
		IsActive:       true,
		CreatedUnix:    timeutil.TimeStamp(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Unix()),
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

	publicRepo := insertLanguageStatsRepo(t, owner, "language-public", false)
	publicRepoPath := gitrepo.RepoLocalPath(gitrepo.CodeRepoByName(owner.Name, publicRepo.Name))
	require.NoError(t, git.InitRepositoryLocal(t.Context(), publicRepoPath, true, git.Sha1ObjectFormat.Name()))
	require.NoError(t, fastImportLanguagePublicHistory(t, publicRepoPath))
	require.NoError(t, gitcmd.NewCommand("branch").AddDynamicArguments("duplicate", "master").
		WithRepo(gitrepo.RepositoryUnmanaged(filepath.Clean(publicRepoPath))).
		Run(t.Context()))

	privateRepo := insertLanguageStatsRepo(t, owner, "language-private", true)
	privateRepoPath := gitrepo.RepoLocalPath(gitrepo.CodeRepoByName(owner.Name, privateRepo.Name))
	require.NoError(t, git.InitRepositoryLocal(t.Context(), privateRepoPath, true, git.Sha1ObjectFormat.Name()))
	require.NoError(t, fastImportLanguagePrivateHistory(t, privateRepoPath))

	result, err := RebuildHistoricalContributions(t.Context(), RebuildHistoricalContributionsOptions{RepoID: publicRepo.ID, UserID: owner.ID, Years: 10})
	require.NoError(t, err)
	assert.EqualValues(t, 1, result.Repos)
	result, err = RebuildHistoricalContributions(t.Context(), RebuildHistoricalContributionsOptions{RepoID: privateRepo.ID, UserID: owner.ID, Years: 10})
	require.NoError(t, err)
	assert.EqualValues(t, 1, result.Repos)
	assert.Equal(t, 6, unittest.GetCount(t, &activities_model.UserContribution{RepoID: publicRepo.ID, UserID: owner.ID}))

	assertLanguageStat(t, owner.ID, 2024, false, "Go", 10, 2, 1)
	assertLanguageStat(t, owner.ID, 2025, false, "Go", 3, 0, 1)
	assertLanguageStat(t, owner.ID, 2025, false, "TypeScript", 4, 4, 2)

	public2026, err := activities_model.GetUserLanguageStats(t.Context(), owner.ID, 2026, false)
	require.NoError(t, err)
	assert.Empty(t, public2026)

	private2026, err := activities_model.GetUserLanguageStats(t.Context(), owner.ID, 2026, true)
	require.NoError(t, err)
	require.Len(t, private2026, 1)
	assert.Equal(t, "Python", private2026[0].Language)
	assert.EqualValues(t, 5, private2026[0].ChangedLines)

	allPublic, err := activities_model.GetUserLanguageStats(t.Context(), owner.ID, 0, false)
	require.NoError(t, err)
	allPublicByLanguage := languageStatsByLanguage(allPublic)
	assert.EqualValues(t, 15, allPublicByLanguage["Go"].ChangedLines)
	assert.EqualValues(t, 8, allPublicByLanguage["TypeScript"].ChangedLines)

	allCombined, err := activities_model.GetUserLanguageStats(t.Context(), owner.ID, 0, true)
	require.NoError(t, err)
	allCombinedByLanguage := languageStatsByLanguage(allCombined)
	assert.EqualValues(t, 5, allCombinedByLanguage["Python"].ChangedLines)
	assert.NotZero(t, unittest.GetCount(t, &activities_model.UserLanguageStat{RepoID: privateRepo.ID, Visibility: activities_model.UserLanguageStatVisibilityPrivate}))

	before := languageSummaryFingerprint(allCombined)
	result, err = RebuildHistoricalContributions(t.Context(), RebuildHistoricalContributionsOptions{RepoID: publicRepo.ID, UserID: owner.ID, Years: 10})
	require.NoError(t, err)
	assert.EqualValues(t, 1, result.Repos)
	result, err = RebuildHistoricalContributions(t.Context(), RebuildHistoricalContributionsOptions{RepoID: privateRepo.ID, UserID: owner.ID, Years: 10})
	require.NoError(t, err)
	assert.EqualValues(t, 1, result.Repos)
	allCombined, err = activities_model.GetUserLanguageStats(t.Context(), owner.ID, 0, true)
	require.NoError(t, err)
	assert.Equal(t, before, languageSummaryFingerprint(allCombined))
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

func insertLanguageStatsRepo(t *testing.T, owner *user_model.User, name string, isPrivate bool) *repo_model.Repository {
	t.Helper()
	repo := &repo_model.Repository{
		OwnerID:          owner.ID,
		OwnerName:        owner.Name,
		LowerName:        strings.ToLower(name),
		Name:             name,
		DefaultBranch:    "master",
		IsEmpty:          false,
		IsPrivate:        isPrivate,
		Status:           repo_model.RepositoryReady,
		ObjectFormatName: git.Sha1ObjectFormat.Name(),
		IsFsckEnabled:    true,
	}
	require.NoError(t, db.Insert(t.Context(), repo))
	return repo
}

type languageImportOp struct {
	op      string
	path    string
	newPath string
	content []byte
}

type languageImportCommit struct {
	mark  int
	from  int
	email string
	when  int64
	ops   []languageImportOp
}

func fastImportLanguagePublicHistory(t *testing.T, repoPath string) error {
	t.Helper()
	commits := []languageImportCommit{
		{
			mark: 1, email: "other@example.com", when: contributionUnix(2024, 1, 1),
			ops: []languageImportOp{{op: "M", path: "a.go", content: []byte("old one\nold two\n")}},
		},
		{
			mark: 2, from: 1, email: "language@example.com", when: contributionUnix(2024, 1, 2),
			ops: []languageImportOp{{op: "M", path: "a.go", content: []byte("line 01\nline 02\nline 03\nline 04\nline 05\nline 06\nline 07\nline 08\nline 09\nline 10\n")}},
		},
		{
			mark: 3, from: 2, email: "language@example.com", when: contributionUnix(2025, 2, 1),
			ops: []languageImportOp{
				{op: "M", path: "one.go", content: []byte("package main\n")},
				{op: "M", path: "two.go", content: []byte("package main\nfunc two() {}\n")},
				{op: "M", path: "frontend.ts", content: []byte("const a = 1;\nconst b = 2;\nconst c = 3;\nexport {a, b, c};\n")},
				{op: "M", path: "vendor/ignored.go", content: []byte("package ignored\nfunc ignored() {}\n")},
				{op: "M", path: "node_modules/ignored.ts", content: []byte("const ignored = true;\n")},
			},
		},
		{
			mark: 4, from: 3, email: "language@example.com", when: contributionUnix(2025, 3, 1),
			ops: []languageImportOp{{op: "R", path: "frontend.ts", newPath: "client.ts"}},
		},
		{
			mark: 5, from: 4, email: "language@example.com", when: contributionUnix(2025, 4, 1),
			ops: []languageImportOp{{op: "D", path: "client.ts"}},
		},
		{
			mark: 6, from: 5, email: "language@example.com", when: contributionUnix(2025, 5, 1),
			ops: []languageImportOp{{op: "M", path: "notes.zzz", content: []byte("not code\njust words\n")}},
		},
		{
			mark: 7, from: 6, email: "language@example.com", when: contributionUnix(2026, 1, 1),
			ops: []languageImportOp{{op: "M", path: "binary.go", content: []byte{0, 1, 2, 3}}},
		},
	}
	return fastImportLanguageHistory(t, repoPath, commits)
}

func fastImportLanguagePrivateHistory(t *testing.T, repoPath string) error {
	t.Helper()
	return fastImportLanguageHistory(t, repoPath, []languageImportCommit{
		{
			mark: 1, email: "language@example.com", when: contributionUnix(2026, 2, 1),
			ops: []languageImportOp{{op: "M", path: "private.py", content: []byte("print(1)\nprint(2)\nprint(3)\nprint(4)\nprint(5)\n")}},
		},
	})
}

func fastImportLanguageHistory(t *testing.T, repoPath string, commits []languageImportCommit) error {
	t.Helper()
	var input strings.Builder
	for _, c := range commits {
		fmt.Fprintf(&input, "commit refs/heads/master\nmark :%d\n", c.mark)
		fmt.Fprintf(&input, "author Test <%s> %d +0000\n", c.email, c.when)
		fmt.Fprintf(&input, "committer Importer <importer@example.com> %d +0000\n", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix())
		message := fmt.Sprintf("language commit %d", c.mark)
		fmt.Fprintf(&input, "data %d\n%s\n", len(message), message)
		if c.from != 0 {
			fmt.Fprintf(&input, "from :%d\n", c.from)
		}
		for _, op := range c.ops {
			switch op.op {
			case "M":
				fmt.Fprintf(&input, "M 100644 inline %s\ndata %d\n", op.path, len(op.content))
				_, _ = input.Write(op.content)
				input.WriteString("\n")
			case "D":
				fmt.Fprintf(&input, "D %s\n", op.path)
			case "R":
				fmt.Fprintf(&input, "R %s %s\n", op.path, op.newPath)
			}
		}
	}
	input.WriteString("done\n")
	return gitcmd.NewCommand("fast-import").AddArguments("--force", "--done").
		WithRepo(gitrepo.RepositoryUnmanaged(filepath.Clean(repoPath))).
		WithStdinBytes([]byte(input.String())).
		Run(t.Context())
}

func assertLanguageStat(t *testing.T, userID int64, year int, includePrivate bool, language string, additions, deletions, commits int64) {
	t.Helper()
	stats, err := activities_model.GetUserLanguageStats(t.Context(), userID, year, includePrivate)
	require.NoError(t, err)
	byLanguage := languageStatsByLanguage(stats)
	stat := byLanguage[language]
	require.NotNil(t, stat)
	assert.Equal(t, additions, stat.Additions)
	assert.Equal(t, deletions, stat.Deletions)
	assert.Equal(t, additions+deletions, stat.ChangedLines)
	assert.Equal(t, commits, stat.Commits)
}

func languageStatsByLanguage(stats []*activities_model.UserLanguageStatSummary) map[string]*activities_model.UserLanguageStatSummary {
	byLanguage := make(map[string]*activities_model.UserLanguageStatSummary, len(stats))
	for _, stat := range stats {
		byLanguage[stat.Language] = stat
	}
	return byLanguage
}

func languageSummaryFingerprint(stats []*activities_model.UserLanguageStatSummary) string {
	var builder strings.Builder
	for _, stat := range stats {
		fmt.Fprintf(&builder, "%s:%d:%d:%d;", stat.Language, stat.Additions, stat.Deletions, stat.Commits)
	}
	return builder.String()
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

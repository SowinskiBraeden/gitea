// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package activities_test

import (
	"testing"
	"time"

	activities_model "gitea.dev/models/activities"
	"gitea.dev/models/db"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/timeutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetUserHeatmapDataByUser(t *testing.T) {
	testCases := []struct {
		desc        string
		userID      int64
		doerID      int64
		CountResult int
		JSONResult  string
	}{
		{
			"self looks at action in private repo",
			2, 2, 1, `[{"timestamp":1603227600,"contributions":1}]`,
		},
		{
			"admin looks at action in private repo",
			2, 1, 1, `[{"timestamp":1603227600,"contributions":1}]`,
		},
		{
			"other user looks at action in private repo",
			2, 3, 0, `[]`,
		},
		{
			"nobody looks at action in private repo",
			2, 0, 0, `[]`,
		},
		{
			"collaborator looks at action in private repo",
			16, 15, 1, `[{"timestamp":1603267200,"contributions":1}]`,
		},
		{
			"no action action not performed by target user",
			3, 3, 0, `[]`,
		},
		{
			"multiple actions performed with two grouped together",
			10, 10, 3, `[{"timestamp":1603009800,"contributions":1},{"timestamp":1603010700,"contributions":2}]`,
		},
	}
	// Prepare
	assert.NoError(t, unittest.PrepareTestDatabase())

	// Mock time
	timeutil.MockSet(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC))
	defer timeutil.MockUnset()

	for _, tc := range testCases {
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: tc.userID})
		user.CreatedUnix = timeutil.TimeStamp(time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC).Unix())

		var doer *user_model.User
		if tc.doerID != 0 {
			doer = unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: tc.doerID})
		}

		// get the action for comparison
		actions, count, err := activities_model.GetFeeds(t.Context(), activities_model.GetFeedsOptions{
			RequestedUser:   user,
			Actor:           doer,
			IncludePrivate:  true,
			OnlyPerformedBy: true,
			IncludeDeleted:  true,
		})
		assert.NoError(t, err)

		// Get the heatmap and compare
		heatmap, err := activities_model.GetUserHeatmapDataByUserAndYear(t.Context(), user, doer, 2020)
		var contributions int
		for _, hm := range heatmap {
			contributions += int(hm.Contributions)
		}
		assert.NoError(t, err)
		assert.Len(t, actions, contributions, "invalid action count: did the test data became too old?")
		assert.Equal(t, count, int64(contributions))
		assert.Equal(t, tc.CountResult, contributions, "testcase '%s'", tc.desc)

		// Test JSON rendering
		jsonData, err := json.Marshal(heatmap)
		assert.NoError(t, err)
		assert.JSONEq(t, tc.JSONResult, string(jsonData))
	}
}

func TestResolveUserHeatmapPeriod(t *testing.T) {
	timeutil.MockSet(time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC))
	defer timeutil.MockUnset()
	oldMaxYears := setting.Service.UserHeatmapMaxYears
	setting.Service.UserHeatmapMaxYears = 10
	defer func() { setting.Service.UserHeatmapMaxYears = oldMaxYears }()

	user := &user_model.User{CreatedUnix: timeutil.TimeStamp(time.Date(2019, 3, 1, 0, 0, 0, 0, time.UTC).Unix())}

	current, err := activities_model.ResolveUserHeatmapPeriod(t.Context(), user, 0)
	require.NoError(t, err)
	assert.Equal(t, 2026, current.Year)
	assert.Equal(t, []int{2026, 2025, 2024, 2023, 2022, 2021, 2020, 2019}, current.Years)

	previous, err := activities_model.ResolveUserHeatmapPeriod(t.Context(), user, 2024)
	require.NoError(t, err)
	assert.Equal(t, 2024, previous.Year)
	assert.Equal(t, time.Date(2024, 1, 1, 0, 0, 0, 0, setting.DefaultUILocation), previous.Start)
	assert.Equal(t, time.Date(2025, 1, 1, 0, 0, 0, 0, setting.DefaultUILocation), previous.End)
	assert.Equal(t, 366, int(previous.End.Sub(previous.Start).Hours()/24))

	for _, requested := range []int{2018, 2027, -1} {
		resolved, err := activities_model.ResolveUserHeatmapPeriod(t.Context(), user, requested)
		require.NoError(t, err)
		assert.Equal(t, 2026, resolved.Year)
	}
}

func TestResolveUserHeatmapPeriodMaxRange(t *testing.T) {
	timeutil.MockSet(time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC))
	defer timeutil.MockUnset()
	oldMaxYears := setting.Service.UserHeatmapMaxYears
	setting.Service.UserHeatmapMaxYears = 3
	defer func() { setting.Service.UserHeatmapMaxYears = oldMaxYears }()

	user := &user_model.User{CreatedUnix: timeutil.TimeStamp(time.Date(2019, 3, 1, 0, 0, 0, 0, time.UTC).Unix())}

	resolved, err := activities_model.ResolveUserHeatmapPeriod(t.Context(), user, 2019)
	require.NoError(t, err)
	assert.Equal(t, 2026, resolved.Year)
	assert.Equal(t, []int{2026, 2025, 2024}, resolved.Years)

	resolved, err = activities_model.ResolveUserHeatmapPeriod(t.Context(), user, 2024)
	require.NoError(t, err)
	assert.Equal(t, 2024, resolved.Year)
}

func TestGetUserHeatmapDataByYearBoundariesAndContributions(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	oldMaxYears := setting.Service.UserHeatmapMaxYears
	setting.Service.UserHeatmapMaxYears = 10
	defer func() { setting.Service.UserHeatmapMaxYears = oldMaxYears }()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	user.CreatedUnix = timeutil.TimeStamp(time.Date(2019, 3, 1, 0, 0, 0, 0, time.UTC).Unix())
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	start := time.Date(2024, 1, 1, 0, 0, 0, 0, setting.DefaultUILocation)
	end := time.Date(2025, 1, 1, 0, 0, 0, 0, setting.DefaultUILocation)
	_, err := db.GetEngine(t.Context()).NoAutoTime().Insert(
		&activities_model.Action{UserID: user.ID, ActUserID: user.ID, RepoID: repo.ID, OpType: activities_model.ActionCreateIssue, CreatedUnix: timeutil.TimeStamp(start.Unix())},
		&activities_model.Action{UserID: user.ID, ActUserID: user.ID, RepoID: repo.ID, OpType: activities_model.ActionCommitRepo, CreatedUnix: timeutil.TimeStamp(start.Add(time.Hour).Unix())},
		&activities_model.Action{UserID: user.ID, ActUserID: user.ID, RepoID: repo.ID, OpType: activities_model.ActionCreateIssue, CreatedUnix: timeutil.TimeStamp(end.Unix())},
	)
	require.NoError(t, err)
	require.NoError(t, activities_model.ReplaceUserContributionsForRepo(t.Context(), repo.ID, []*activities_model.UserContribution{
		{
			UserID:           user.ID,
			RepoID:           repo.ID,
			CommitSHA:        "0123456789012345678901234567890123456789",
			ContributionType: activities_model.UserContributionTypeCommit,
			ContributionUnix: timeutil.TimeStamp(end.Add(-time.Second).Unix()),
		},
	}))

	heatmap, err := activities_model.GetUserHeatmapDataByUserAndYear(t.Context(), user, user, 2024)
	require.NoError(t, err)
	var total int64
	for _, item := range heatmap {
		total += item.Contributions
	}
	assert.EqualValues(t, 2, total)
}

func TestGetUserHeatmapDataPrivateContributions(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, db.TruncateBeans(t.Context(), &activities_model.Action{}, &activities_model.UserContribution{}))
	oldMaxYears := setting.Service.UserHeatmapMaxYears
	setting.Service.UserHeatmapMaxYears = 10
	defer func() { setting.Service.UserHeatmapMaxYears = oldMaxYears }()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	user.CreatedUnix = timeutil.TimeStamp(time.Date(2019, 3, 1, 0, 0, 0, 0, time.UTC).Unix())
	publicRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	privateRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	require.False(t, publicRepo.IsPrivate)
	require.True(t, privateRepo.IsPrivate)

	when := time.Date(2024, 9, 9, 12, 0, 0, 0, time.UTC)
	require.NoError(t, activities_model.ReplaceUserContributionsForRepo(t.Context(), publicRepo.ID, []*activities_model.UserContribution{
		{
			UserID:           user.ID,
			RepoID:           publicRepo.ID,
			CommitSHA:        "1111111111111111111111111111111111111111",
			ContributionType: activities_model.UserContributionTypeCommit,
			ContributionUnix: timeutil.TimeStamp(when.Unix()),
		},
	}))
	require.NoError(t, activities_model.ReplaceUserContributionsForRepo(t.Context(), privateRepo.ID, []*activities_model.UserContribution{
		{
			UserID:           user.ID,
			RepoID:           privateRepo.ID,
			CommitSHA:        "2222222222222222222222222222222222222222",
			ContributionType: activities_model.UserContributionTypeCommit,
			ContributionUnix: timeutil.TimeStamp(when.Unix()),
		},
		{
			UserID:           user.ID,
			RepoID:           privateRepo.ID,
			CommitSHA:        "3333333333333333333333333333333333333333",
			ContributionType: activities_model.UserContributionTypeCommit,
			ContributionUnix: timeutil.TimeStamp(when.Add(time.Minute).Unix()),
		},
	}))

	require.NoError(t, user_model.SetUserSetting(t.Context(), user.ID, user_model.SettingsKeyContributionShowPrivate, "false"))
	anonymousHeatmap, err := activities_model.GetUserHeatmapDataByUserAndYear(t.Context(), user, nil, 2024)
	require.NoError(t, err)
	assert.EqualValues(t, 1, heatmapTotal(anonymousHeatmap))

	ownerHeatmap, err := activities_model.GetUserHeatmapDataByUserAndYear(t.Context(), user, user, 2024)
	require.NoError(t, err)
	assert.EqualValues(t, 3, heatmapTotal(ownerHeatmap))

	require.NoError(t, user_model.SetUserSetting(t.Context(), user.ID, user_model.SettingsKeyContributionShowPrivate, "true"))
	anonymousHeatmap, err = activities_model.GetUserHeatmapDataByUserAndYear(t.Context(), user, nil, 2024)
	require.NoError(t, err)
	assert.EqualValues(t, 3, heatmapTotal(anonymousHeatmap))

	jsonData, err := json.Marshal(anonymousHeatmap)
	require.NoError(t, err)
	assert.NotContains(t, string(jsonData), publicRepo.Name)
	assert.NotContains(t, string(jsonData), privateRepo.Name)
	assert.NotContains(t, string(jsonData), "2222222")
	assert.NotContains(t, string(jsonData), "3333333")
}

func TestGetUserContributionFeedItems(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, db.TruncateBeans(t.Context(), &activities_model.UserContribution{}))

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	publicRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	privateRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	when := time.Date(2024, 9, 10, 12, 0, 0, 0, setting.DefaultUILocation)

	require.NoError(t, activities_model.ReplaceUserContributionsForRepo(t.Context(), publicRepo.ID, []*activities_model.UserContribution{
		{
			UserID:           user.ID,
			RepoID:           publicRepo.ID,
			CommitSHA:        "1111111111111111111111111111111111111111",
			ContributionType: activities_model.UserContributionTypeCommit,
			ContributionUnix: timeutil.TimeStamp(when.Unix()),
		},
	}))
	require.NoError(t, activities_model.ReplaceUserContributionsForRepo(t.Context(), privateRepo.ID, []*activities_model.UserContribution{
		{
			UserID:           user.ID,
			RepoID:           privateRepo.ID,
			CommitSHA:        "2222222222222222222222222222222222222222",
			ContributionType: activities_model.UserContributionTypeCommit,
			ContributionUnix: timeutil.TimeStamp(when.Add(time.Hour).Unix()),
		},
		{
			UserID:           user.ID,
			RepoID:           privateRepo.ID,
			CommitSHA:        "3333333333333333333333333333333333333333",
			ContributionType: activities_model.UserContributionTypeCommit,
			ContributionUnix: timeutil.TimeStamp(when.Add(2 * time.Hour).Unix()),
		},
	}))

	require.NoError(t, user_model.SetUserSetting(t.Context(), user.ID, user_model.SettingsKeyContributionShowPrivate, "false"))
	anonymousItems, err := activities_model.GetUserContributionFeedItems(t.Context(), user, nil, "2024-09-10")
	require.NoError(t, err)
	require.Len(t, anonymousItems, 1)
	assert.Equal(t, publicRepo.ID, anonymousItems[0].RepoID)
	assert.EqualValues(t, 1, anonymousItems[0].Count)
	assert.False(t, anonymousItems[0].IsPrivate)

	ownerItems, err := activities_model.GetUserContributionFeedItems(t.Context(), user, user, "2024-09-10")
	require.NoError(t, err)
	require.Len(t, ownerItems, 2)
	assert.Equal(t, publicRepo.ID, ownerItems[0].RepoID)
	assert.True(t, ownerItems[1].IsPrivate)
	assert.EqualValues(t, 2, ownerItems[1].Count)
	assert.Nil(t, ownerItems[1].Repo)

	require.NoError(t, user_model.SetUserSetting(t.Context(), user.ID, user_model.SettingsKeyContributionShowPrivate, "true"))
	anonymousItems, err = activities_model.GetUserContributionFeedItems(t.Context(), user, nil, "2024-09-10")
	require.NoError(t, err)
	require.Len(t, anonymousItems, 2)
	assert.Equal(t, publicRepo.ID, anonymousItems[0].RepoID)
	assert.True(t, anonymousItems[1].IsPrivate)
	assert.EqualValues(t, 2, anonymousItems[1].Count)
	assert.Nil(t, anonymousItems[1].Repo)
}

func heatmapTotal(data []*activities_model.UserHeatmapData) int64 {
	var total int64
	for _, item := range data {
		total += item.Contributions
	}
	return total
}

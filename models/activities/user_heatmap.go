// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package activities

import (
	"context"
	"slices"
	"time"

	"gitea.dev/models/db"
	"gitea.dev/models/organization"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/timeutil"

	"xorm.io/builder"
)

// UserHeatmapData represents the data needed to create a heatmap
type UserHeatmapData struct {
	Timestamp     timeutil.TimeStamp `json:"timestamp"`
	Contributions int64              `json:"contributions"`
}

type UserHeatmapPeriod struct {
	Year  int
	Start time.Time
	End   time.Time
	Years []int
}

// GetUserHeatmapDataByUser returns an array of UserHeatmapData, it checks whether doer can access user's activity
func GetUserHeatmapDataByUser(ctx context.Context, user, doer *user_model.User) ([]*UserHeatmapData, error) {
	return GetUserHeatmapDataByUserAndYear(ctx, user, doer, 0)
}

func GetUserHeatmapDataByUserAndYear(ctx context.Context, user, doer *user_model.User, year int) ([]*UserHeatmapData, error) {
	period := ResolveUserHeatmapPeriod(user, year)
	return getUserHeatmapData(ctx, user, nil, doer, period.Start, period.End)
}

// GetUserHeatmapDataByOrgTeam returns an array of UserHeatmapData, it checks whether doer can access org's activity
func GetUserHeatmapDataByOrgTeam(ctx context.Context, org *organization.Organization, team *organization.Team, doer *user_model.User) ([]*UserHeatmapData, error) {
	return GetUserHeatmapDataByOrgTeamAndYear(ctx, org, team, doer, 0)
}

func GetUserHeatmapDataByOrgTeamAndYear(ctx context.Context, org *organization.Organization, team *organization.Team, doer *user_model.User, year int) ([]*UserHeatmapData, error) {
	period := ResolveUserHeatmapPeriod(org.AsUser(), year)
	return getUserHeatmapData(ctx, org.AsUser(), team, doer, period.Start, period.End)
}

func ResolveUserHeatmapPeriod(user *user_model.User, year int) UserHeatmapPeriod {
	now := timeutil.TimeStampNow().AsTime()
	currentYear := now.Year()
	firstYear := currentYear
	if user != nil && !user.CreatedUnix.IsZero() {
		firstYear = user.CreatedUnix.AsTime().Year()
	} else if year > 0 && year <= currentYear {
		firstYear = year
	}
	if firstYear > currentYear {
		firstYear = currentYear
	}
	if year < firstYear || year > currentYear {
		year = currentYear
	}

	years := make([]int, 0, currentYear-firstYear+1)
	for y := currentYear; y >= firstYear; y-- {
		years = append(years, y)
	}

	start := time.Date(year, 1, 1, 0, 0, 0, 0, setting.DefaultUILocation)
	return UserHeatmapPeriod{
		Year:  year,
		Start: start,
		End:   start.AddDate(1, 0, 0),
		Years: years,
	}
}

func getUserHeatmapData(ctx context.Context, user *user_model.User, team *organization.Team, doer *user_model.User, start, end time.Time) ([]*UserHeatmapData, error) {
	hdata := make([]*UserHeatmapData, 0)

	if !ActivityReadable(user, doer) {
		return hdata, nil
	}

	// Group by 15 minute intervals which will allow the client to accurately shift the timestamp to their timezone.
	// The interval is based on the fact that there are timezones such as UTC +5:30 and UTC +12:45.
	groupBy, groupByName := heatmapGroupBy("created_unix")

	cond, err := ActivityQueryCondition(ctx, GetFeedsOptions{
		RequestedUser:  user,
		RequestedTeam:  team,
		Actor:          doer,
		IncludePrivate: true, // don't filter by private, as we already filter by repo access
		IncludeDeleted: true,
		// * Heatmaps for individual users only include actions that the user themself did.
		// * For organizations actions by all users that were made in owned
		//   repositories are counted.
		OnlyPerformedBy: !user.IsOrganization(),
	})
	if err != nil {
		return nil, err
	}
	if !user.IsOrganization() {
		cond = cond.And(builder.NotIn("`action`.op_type", ActionCommitRepo, ActionMirrorSyncPush))
	}

	err = db.GetEngine(ctx).
		Select(groupBy+" AS timestamp, count(user_id) as contributions").
		Table("action").
		Where(cond).
		And("created_unix >= ?", start.Unix()).
		And("created_unix < ?", end.Unix()).
		GroupBy(groupByName).
		OrderBy("timestamp").
		Find(&hdata)
	if err != nil {
		return nil, err
	}
	contributionData, err := getUserContributionHeatmapData(ctx, user, doer, start, end)
	if err != nil {
		return nil, err
	}
	return mergeUserHeatmapData(hdata, contributionData), nil
}

func mergeUserHeatmapData(left, right []*UserHeatmapData) []*UserHeatmapData {
	byTimestamp := make(map[timeutil.TimeStamp]int64, len(left)+len(right))
	for _, item := range slices.Concat(left, right) {
		byTimestamp[item.Timestamp] += item.Contributions
	}
	merged := make([]*UserHeatmapData, 0, len(byTimestamp))
	for timestamp, contributions := range byTimestamp {
		merged = append(merged, &UserHeatmapData{Timestamp: timestamp, Contributions: contributions})
	}
	slices.SortFunc(merged, func(a, b *UserHeatmapData) int {
		return int(a.Timestamp - b.Timestamp)
	})
	return merged
}

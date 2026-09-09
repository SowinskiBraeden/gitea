// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package user

import (
	"net/http"
	"net/url"
	"strconv"

	activities_model "gitea.dev/models/activities"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	"gitea.dev/services/context"
)

func heatmapSubject(ctx *context.Context) *user_model.User {
	if ctx.Org.Organization != nil {
		return ctx.Org.Organization.AsUser()
	}
	if ctx.ContextUser != nil {
		return ctx.ContextUser
	}
	return ctx.Doer
}

func heatmapPeriod(ctx *context.Context, year int) activities_model.UserHeatmapPeriod {
	return activities_model.ResolveUserHeatmapPeriod(heatmapSubject(ctx), year)
}

func appendHeatmapYear(rawURL string, year int) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := u.Query()
	q.Set("year", strconv.Itoa(year))
	u.RawQuery = q.Encode()
	return u.String()
}

func prepareHeatmapYears(ctx *context.Context, baseURL string, period activities_model.UserHeatmapPeriod) {
	type heatmapYearLink struct {
		Year     int
		URL      string
		Selected bool
	}
	links := make([]heatmapYearLink, 0, len(period.Years))
	subject := heatmapSubject(ctx)
	for _, year := range period.Years {
		linkURL := subject.HomeLink() + "?tab=activity&year=" + strconv.Itoa(year)
		if ctx.Org.Organization != nil {
			linkURL = appendHeatmapYear(baseURL, year)
		}
		links = append(links, heatmapYearLink{
			Year:     year,
			URL:      linkURL,
			Selected: year == period.Year,
		})
	}
	ctx.Data["HeatmapYears"] = links
	ctx.Data["HeatmapSelectedYear"] = period.Year
}

func prepareHeatmapURL(ctx *context.Context) {
	ctx.Data["EnableHeatmap"] = setting.Service.EnableUserHeatmap
	if !setting.Service.EnableUserHeatmap {
		return
	}

	period := heatmapPeriod(ctx, ctx.FormInt("year"))
	if ctx.Org.Organization == nil {
		// for individual user
		heatmapURL := appendHeatmapYear(ctx.Doer.HomeLink()+"/-/heatmap", period.Year)
		ctx.Data["HeatmapURL"] = heatmapURL
		prepareHeatmapYears(ctx, heatmapURL, period)
		return
	}

	// for org or team
	heatmapURL := ctx.Org.Organization.OrganisationLink() + "/dashboard/-/heatmap"
	if ctx.Org.Team != nil {
		heatmapURL += "/" + url.PathEscape(ctx.Org.Team.LowerName)
	}
	heatmapURL = appendHeatmapYear(heatmapURL, period.Year)
	ctx.Data["HeatmapURL"] = heatmapURL
	prepareHeatmapYears(ctx, heatmapURL, period)
}

func writeHeatmapJSON(ctx *context.Context, hdata []*activities_model.UserHeatmapData, period activities_model.UserHeatmapPeriod) {
	data := make([][2]int64, len(hdata))
	var total int64
	for i, v := range hdata {
		data[i] = [2]int64{int64(v.Timestamp), v.Contributions}
		total += v.Contributions
	}
	ctx.JSON(http.StatusOK, map[string]any{
		"heatmapData":        data,
		"totalContributions": total,
		"year":               period.Year,
		"startDate":          period.Start.Format("2006-01-02"),
		"endDate":            period.End.AddDate(0, 0, -1).Format("2006-01-02"),
	})
}

// DashboardHeatmap returns heatmap data as JSON, for the individual user, organization or team dashboard.
func DashboardHeatmap(ctx *context.Context) {
	if !setting.Service.EnableUserHeatmap {
		ctx.NotFound(nil)
		return
	}
	var data []*activities_model.UserHeatmapData
	var err error
	period := heatmapPeriod(ctx, ctx.FormInt("year"))
	if ctx.Org.Organization == nil {
		data, err = activities_model.GetUserHeatmapDataByUserAndYear(ctx, heatmapSubject(ctx), ctx.Doer, period.Year)
	} else {
		data, err = activities_model.GetUserHeatmapDataByOrgTeamAndYear(ctx, ctx.Org.Organization, ctx.Org.Team, ctx.Doer, period.Year)
	}
	if err != nil {
		ctx.ServerError("GetUserHeatmapData", err)
		return
	}
	writeHeatmapJSON(ctx, data, period)
}

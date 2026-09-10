// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package setting

import (
	"net/http"
	"strconv"

	activities_model "gitea.dev/models/activities"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/templates"
	"gitea.dev/modules/web"
	"gitea.dev/services/context"
	"gitea.dev/services/forms"
	repo_service "gitea.dev/services/repository"
)

const tplSettingsContributions templates.TplName = "user/settings/contributions"

func Contributions(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("settings.contributions")
	ctx.Data["PageIsSettingsContributions"] = true

	showPrivate, err := activities_model.UserShowsPrivateContributions(ctx, ctx.Doer)
	if err != nil {
		ctx.ServerError("UserShowsPrivateContributions", err)
		return
	}
	years, err := activities_model.UserContributionHistoryYears(ctx, ctx.Doer)
	if err != nil {
		ctx.ServerError("UserContributionHistoryYears", err)
		return
	}
	ctx.Data["ContributionShowPrivate"] = showPrivate
	ctx.Data["ContributionHistoryYears"] = years
	ctx.Data["ContributionHistoryMaxYears"] = setting.Service.UserHeatmapMaxYears

	ctx.HTML(http.StatusOK, tplSettingsContributions)
}

func ContributionsPost(ctx *context.Context) {
	if ctx.HasError() {
		Contributions(ctx)
		return
	}
	form := web.GetForm[*forms.UpdateContributionSettingsForm](ctx)
	years := repo_service.ParseContributionHistoryYears(strconv.Itoa(form.Years))
	if years < 1 {
		ctx.Flash.Error(ctx.Tr("settings.contribution_history_years_invalid"))
		ctx.Redirect(setting.AppSubURL + "/user/settings/contributions")
		return
	}
	if err := user_model.SetUserSetting(ctx, ctx.Doer.ID, user_model.SettingsKeyContributionShowPrivate, strconv.FormatBool(form.ShowPrivate)); err != nil {
		ctx.ServerError("SetUserSetting", err)
		return
	}
	if err := user_model.SetUserSetting(ctx, ctx.Doer.ID, user_model.SettingsKeyContributionHistoryYears, strconv.Itoa(years)); err != nil {
		ctx.ServerError("SetUserSetting", err)
		return
	}
	ctx.Flash.Success(ctx.Tr("settings.saved_successfully"))
	ctx.Redirect(setting.AppSubURL + "/user/settings/contributions")
}

func ContributionsRebuildPost(ctx *context.Context) {
	queued, err := repo_service.AddUserToHistoricalContributionsQueue(ctx.Doer.ID)
	if err != nil {
		ctx.ServerError("AddUserToHistoricalContributionsQueue", err)
		return
	}
	if queued {
		ctx.Flash.Success(ctx.Tr("settings.contribution_history_rebuild_started"))
	} else {
		ctx.Flash.Info(ctx.Tr("settings.contribution_history_rebuild_already_running"))
	}
	ctx.Redirect(setting.AppSubURL + "/user/settings/contributions")
}

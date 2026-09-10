// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/git"
	repo_service "gitea.dev/services/repository"

	"github.com/urfave/cli/v3"
)

func newRebuildContributionsCommand() *cli.Command {
	return &cli.Command{
		Name:   "rebuild-contributions",
		Usage:  "Rebuild historical Git contributions from repository default branches",
		Action: runRebuildContributions,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "repo",
				Usage: "Only rebuild one repository, as owner/name or numeric repository ID",
			},
			&cli.StringFlag{
				Name:  "user",
				Usage: "Only rebuild contributions for one username",
			},
			&cli.IntFlag{
				Name:  "years",
				Usage: "Maximum number of calendar years to scan",
			},
		},
	}
}

func runRebuildContributions(ctx context.Context, c *cli.Command) error {
	if err := initDB(ctx); err != nil {
		return err
	}
	if err := git.InitSimple(); err != nil {
		return err
	}

	var repoID, userID int64
	if repoArg := strings.TrimSpace(c.String("repo")); repoArg != "" {
		if parsedID, err := strconv.ParseInt(repoArg, 10, 64); err == nil {
			repoID = parsedID
		} else {
			ownerName, repoName, ok := strings.Cut(repoArg, "/")
			if !ok || ownerName == "" || repoName == "" {
				return errors.New("--repo must be owner/name or numeric repository ID")
			}
			repo, err := repo_model.GetRepositoryByOwnerAndName(ctx, ownerName, repoName)
			if err != nil {
				return err
			}
			repoID = repo.ID
		}
	}
	if userArg := strings.TrimSpace(c.String("user")); userArg != "" {
		u, err := user_model.GetUserByName(ctx, userArg)
		if err != nil {
			return err
		}
		userID = u.ID
		cprintf(c, "Resolving contributions for %s\n", u.Name)
	} else {
		cprintf(c, "Resolving historical contributions\n")
	}
	cprintf(c, "Scanning repositories...\n")

	result, err := repo_service.RebuildHistoricalContributions(ctx, repo_service.RebuildHistoricalContributionsOptions{
		RepoID: repoID,
		UserID: userID,
		Years:  c.Int("years"),
	})
	if err != nil {
		return err
	}
	years := make([]int, 0, len(result.ByYear))
	for year := range result.ByYear {
		years = append(years, year)
	}
	slices.SortFunc(years, func(a, b int) int {
		return b - a
	})
	for _, year := range years {
		cprintf(c, "%d: %d contributions\n", year, result.ByYear[year])
	}
	cprintf(c, "Rebuilt historical contributions for %d repositories: %d contributions\n", result.Repos, result.Contributions)
	cprintf(c, "Complete.\n")
	return nil
}

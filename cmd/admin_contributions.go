// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"errors"
	"strconv"
	"strings"

	repo_model "gitea.dev/models/repo"
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

	var repoID int64
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

	result, err := repo_service.RebuildHistoricalContributions(ctx, repo_service.RebuildHistoricalContributionsOptions{RepoID: repoID})
	if err != nil {
		return err
	}
	cprintf(c, "Rebuilt historical contributions for %d repositories: %d contributions\n", result.Repos, result.Contributions)
	return nil
}

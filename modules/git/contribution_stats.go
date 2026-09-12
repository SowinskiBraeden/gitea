// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gitea.dev/modules/git/gitcmd"
)

// CommitFileStat contains line-change statistics for one path changed by a commit.
type CommitFileStat struct {
	Path      string
	OldPath   string
	Additions int64
	Deletions int64
	IsBinary  bool
}

// GetCommitFileStats returns Git's numstat rows for a commit.
func GetCommitFileStats(ctx context.Context, repo *Repository, commitSHA string) ([]CommitFileStat, error) {
	stdout, _, err := gitcmd.NewCommand("diff-tree", "--numstat", "-z", "--find-renames", "--root", "--no-commit-id", "-r").
		AddDynamicArguments(commitSHA).
		WithRepo(repo).
		RunStdString(ctx)
	if err != nil {
		return nil, err
	}
	if stdout == "" {
		return nil, nil
	}

	tokens := strings.Split(stdout, "\x00")
	if tokens[len(tokens)-1] == "" {
		tokens = tokens[:len(tokens)-1]
	}

	stats := make([]CommitFileStat, 0, len(tokens))
	for i := 0; i < len(tokens); i++ {
		parts := strings.Split(tokens[i], "\t")
		if len(parts) != 3 {
			return nil, fmt.Errorf("unexpected numstat record with %d fields", len(parts))
		}

		stat := CommitFileStat{Path: parts[2]}
		if stat.Path == "" {
			if i+2 >= len(tokens) {
				return nil, errors.New("unexpected rename numstat record")
			}
			stat.OldPath = tokens[i+1]
			stat.Path = tokens[i+2]
			i += 2
		}

		if parts[0] == "-" || parts[1] == "-" {
			stat.IsBinary = true
			stats = append(stats, stat)
			continue
		}

		additions, parseErr := strconv.ParseInt(parts[0], 10, 64)
		if parseErr != nil {
			return nil, parseErr
		}
		deletions, parseErr := strconv.ParseInt(parts[1], 10, 64)
		if parseErr != nil {
			return nil, parseErr
		}
		stat.Additions = additions
		stat.Deletions = deletions
		stats = append(stats, stat)
	}
	return stats, nil
}

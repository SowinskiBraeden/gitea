// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"gitea.dev/modules/git/gitcmd"
)

// CommitAuthor contains the commit identity fields needed for contribution attribution.
type CommitAuthor struct {
	SHA   string
	Email string
	When  time.Time
}

// WalkCommitAuthors walks commits reachable from revision and calls walkFn for each author identity.
func WalkCommitAuthors(ctx context.Context, repo RepositoryFacade, revision string, walkFn func(CommitAuthor) error) error {
	return WalkCommitAuthorsSince(ctx, repo, revision, time.Time{}, walkFn)
}

// WalkCommitAuthorsSince walks commits reachable from revision and calls walkFn for each author identity.
func WalkCommitAuthorsSince(ctx context.Context, repo RepositoryFacade, revision string, since time.Time, walkFn func(CommitAuthor) error) error {
	gitCmd := gitcmd.NewCommand("log", "--format=%H%x00%aE%x00%at%x1e")
	if !since.IsZero() {
		gitCmd.AddOptionFormat("--since=%s", since.Format(time.RFC3339))
	}
	gitCmd.AddDynamicArguments(revision)

	stdoutReader, stdoutReaderClose := gitCmd.MakeStdoutPipe()
	defer stdoutReaderClose()

	return gitCmd.WithRepo(repo).
		WithPipelineFunc(func(ctx gitcmd.Context) error {
			reader := bufio.NewReader(stdoutReader)
			for {
				record, err := reader.ReadBytes(0x1e)
				if err != nil && err != io.EOF {
					return err
				}
				record = bytes.TrimSuffix(record, []byte{0x1e})
				record = bytes.TrimSpace(record)
				if len(record) > 0 {
					parts := bytes.Split(record, []byte{0})
					if len(parts) != 3 {
						return fmt.Errorf("unexpected git log contribution record with %d fields", len(parts))
					}
					authorUnix, parseErr := strconv.ParseInt(strings.TrimSpace(string(parts[2])), 10, 64)
					if parseErr != nil {
						return parseErr
					}
					if err := walkFn(CommitAuthor{
						SHA:   strings.TrimSpace(string(parts[0])),
						Email: strings.TrimSpace(string(parts[1])),
						When:  time.Unix(authorUnix, 0),
					}); err != nil {
						return err
					}
				}
				if err == io.EOF {
					return nil
				}
			}
		}).
		RunWithStderr(ctx)
}

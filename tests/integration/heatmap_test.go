// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"testing"
	"time"

	"gitea.dev/models/db"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/timeutil"
	"gitea.dev/tests"

	"github.com/stretchr/testify/assert"
)

func TestHeatmapEndpoints(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	// Mock time so fixture actions fall within the heatmap's time window
	timeutil.MockSet(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC))
	defer timeutil.MockUnset()

	session := loginUser(t, "user2")

	t.Run("UserProfile", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		req := NewRequest(t, "GET", "/user2/-/heatmap?year=2020")
		resp := session.MakeRequest(t, req, http.StatusOK)

		result := DecodeJSON(t, resp, map[string]any{})
		assert.Contains(t, result, "heatmapData")
		assert.Contains(t, result, "totalContributions")
		assert.Positive(t, result["totalContributions"])
	})

	t.Run("ProfileYearNavigation", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: "user2"})
		user.CreatedUnix = timeutil.TimeStamp(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Unix())
		_, err := db.GetEngine(t.Context()).ID(user.ID).Cols("created_unix").Update(user)
		assert.NoError(t, err)

		req := NewRequest(t, "GET", "/user2?tab=activity&year=2020")
		resp := session.MakeRequest(t, req, http.StatusOK)

		doc := NewHTMLParser(t, resp.Body)
		AssertHTMLElement(t, doc, ".heatmap-year-nav strong", true)
		AssertHTMLElement(t, doc, ".heatmap-year-nav a", true)
		assert.Equal(t, 1, doc.doc.Find(".heatmap-year-nav strong").Length())
		assert.LessOrEqual(t, doc.doc.Find(".heatmap-year-nav a").Length(), 2)
	})

	t.Run("OrgDashboard", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		req := NewRequest(t, "GET", "/org/org3/dashboard/-/heatmap?year=2020")
		resp := session.MakeRequest(t, req, http.StatusOK)

		result := DecodeJSON(t, resp, map[string]any{})
		assert.Contains(t, result, "heatmapData")
		assert.Contains(t, result, "totalContributions")
	})

	t.Run("OrgTeamDashboard", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		req := NewRequest(t, "GET", "/org/org3/dashboard/-/heatmap/team1?year=2020")
		resp := session.MakeRequest(t, req, http.StatusOK)

		result := DecodeJSON(t, resp, map[string]any{})
		assert.Contains(t, result, "heatmapData")
		assert.Contains(t, result, "totalContributions")
	})
}

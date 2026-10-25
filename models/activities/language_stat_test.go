// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package activities

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsGenericLanguage(t *testing.T) {
	for _, language := range []string{"CSS", "SCSS", "HTML", "EJS", "Markdown", "JSON", "YAML"} {
		assert.True(t, isGenericLanguage(language), language)
	}
	for _, language := range []string{"Go", "TypeScript", "JavaScript", "Python", "Elixir", "Rust"} {
		assert.False(t, isGenericLanguage(language), language)
	}
}

func TestDominatedByJavaScript(t *testing.T) {
	assert.False(t, dominatedByJavaScript(nil))
	assert.False(t, dominatedByJavaScript([]*UserLanguageStatSummary{{Language: "Go"}, {Language: "JavaScript"}}))
	assert.True(t, dominatedByJavaScript([]*UserLanguageStatSummary{{Language: "JavaScript"}, {Language: "Go"}}))
}

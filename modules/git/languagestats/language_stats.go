// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package languagestats

import (
	"context"
	"strings"
	"unicode"

	"gitea.dev/modules/analyze"
	"gitea.dev/modules/git"
	"gitea.dev/modules/git/attribute"
	"gitea.dev/modules/optional"

	"github.com/go-enry/go-enry/v2"
)

const (
	fileSizeLimit int64 = 16 * 1024   // 16 KiB
	bigFileSize   int64 = 1024 * 1024 // 1 MiB
)

// mergeLanguageStats mergers language names with different cases. The name with most upper case letters is used.
func mergeLanguageStats(stats map[string]int64) map[string]int64 {
	names := map[string]struct {
		uniqueName string
		upperCount int
	}{}

	countUpper := func(s string) (count int) {
		for _, r := range s {
			if unicode.IsUpper(r) {
				count++
			}
		}
		return count
	}

	for name := range stats {
		cnt := countUpper(name)
		lower := strings.ToLower(name)
		if cnt >= names[lower].upperCount {
			names[lower] = struct {
				uniqueName string
				upperCount int
			}{uniqueName: name, upperCount: cnt}
		}
	}

	res := make(map[string]int64, len(names))
	for name, num := range stats {
		res[names[strings.ToLower(name)].uniqueName] += num
	}
	return res
}

// GetFileLanguage tries to get the (linguist) language of the file content
func GetFileLanguage(ctx context.Context, gitRepo *git.Repository, treeish, treePath string) (string, error) {
	attributesMap, err := attribute.CheckAttributes(ctx, gitRepo, treeish, attribute.CheckAttributeOpts{
		Attributes: []string{attribute.LinguistLanguage, attribute.GitlabLanguage},
		Filenames:  []string{treePath},
	})
	if err != nil {
		return "", err
	}

	return attributesMap[treePath].GetLanguage().Value(), nil
}

// DetectFileLanguage applies the same Linguist/Enry inclusion rules used by repository language stats.
func DetectFileLanguage(treePath string, content []byte, attrs *attribute.Attributes) string {
	isVendored := optional.None[bool]()
	isDocumentation := optional.None[bool]()
	isDetectable := optional.None[bool]()
	attrLinguistGenerated := optional.None[bool]()

	if attrs != nil {
		if isVendored = attrs.GetVendored(); isVendored.ValueOrDefault(false) {
			return ""
		}

		if attrLinguistGenerated = attrs.GetGenerated(); attrLinguistGenerated.ValueOrDefault(false) {
			return ""
		}

		if isDocumentation = attrs.GetDocumentation(); isDocumentation.ValueOrDefault(false) {
			return ""
		}

		if isDetectable = attrs.GetDetectable(); !isDetectable.ValueOrDefault(true) {
			return ""
		}

		if hasLanguage := attrs.GetLanguage(); hasLanguage.Value() != "" {
			return normalizeLanguageName(hasLanguage.Value())
		}
	}

	if (!isVendored.Has() && analyze.IsVendor(treePath)) ||
		enry.IsDotFile(treePath) ||
		(!isDocumentation.Has() && enry.IsDocumentation(treePath)) ||
		(!isDetectable.Has() && enry.IsConfiguration(treePath)) {
		return ""
	}

	var isGenerated bool
	if attrLinguistGenerated.Has() {
		isGenerated = attrLinguistGenerated.Value()
	} else {
		isGenerated = enry.IsGenerated(treePath, content)
	}
	if isGenerated {
		return ""
	}

	language := analyze.GetCodeLanguage(treePath, content)
	if language == "" {
		return ""
	}
	language = normalizeLanguageName(language)
	langType := enry.GetLanguageType(language)
	if langType == enry.Programming || langType == enry.Markup || isDetectable.ValueOrDefault(false) {
		return language
	}
	return ""
}

func normalizeLanguageName(language string) string {
	if canonicalLanguage, ok := enry.GetLanguageByAlias(language); ok {
		language = canonicalLanguage
	}
	if group := enry.GetLanguageGroup(language); group != "" {
		language = group
	}
	return language
}

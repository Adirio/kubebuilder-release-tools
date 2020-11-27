/*
Copyright 2020 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"fmt"

	"github.com/google/go-github/v32/github"

	notes "sigs.k8s.io/kubebuilder-release-tools/notes/common"
)

type prTitleTypeError struct {
	title string
}

func (e prTitleTypeError) Error() string {
	return "no matching PR type indicator found in title"
}
func (e prTitleTypeError) Details() string {
	return fmt.Sprintf(
		`I saw a title of %#q, which doesn't seem to have any of the acceptable prefixes.

You need to have one of these as the prefix of your PR title:

- Breaking change: ⚠ (%#q)
- Non-breaking feature: ✨ (%#q)
- Patch fix: 🐛 (%#q)
- Docs: 📖 (%#q)
- Infra/Tests/Other: 🌱 (%#q)

More details can be found at [sigs.k8s.io/kubebuilder-release-tools/VERSIONING.md](https://sigs.k8s.io/kubebuilder-release-tools/VERSIONING.md).`,
		e.title, ":warning:", ":sparkles:", ":bug:", ":book:", ":seedling:")
}

// verifyPRType checks that the PR title contains a prefix that defines its type
func verifyPRType(pr *github.PullRequest) (string, error) {
	prType, finalTitle := notes.PRTypeFromTitle(pr.GetTitle())
	if prType == notes.UncategorizedPR {
		return "", prTitleTypeError{title: pr.GetTitle()}
	}

	return fmt.Sprintf(
		`Found %s PR (%s) with final title:

	%s
`,
		prType.Emoji(), prType, finalTitle,
	), nil
}

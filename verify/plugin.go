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

package verify

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/go-github/v32/github"
)

// ErrorWithHelp allows PRPlugin.ProcessPR to provide extended descriptions
type ErrorWithHelp interface {
	error
	Help() string
}

// PRPlugin handles pull request events
type PRPlugin struct {
	ProcessPR func(pr *github.PullRequest) (string, error)
	Name      string
	Title     string

	logger
}

// processPR executes the provided ProcessPR and parses the result
func (p PRPlugin) processPR(pr *github.PullRequest) (conclusion, summary, text string, err error) {
	text, err = p.ProcessPR(pr)

	if err != nil {
		conclusion = "failure"
		summary = err.Error()
		var helpErr ErrorWithHelp
		if errors.As(err, &helpErr) {
			text = helpErr.Help()
		}
	} else {
		conclusion = "success"
		summary = "Success"
	}

	// Log in case we can't submit the result for some reason
	p.debugf("plugin conclusion: %q", conclusion)
	p.debugf("plugin result summary: %q", summary)
	p.debugf("plugin result details: %q", text)

	return conclusion, summary, text, err
}

// processAndSubmit performs the checks and submits the result
func (p PRPlugin) processAndSubmit(env *ActionsEnv, checkRun *github.CheckRun) (*github.CheckRun, error) {
	// Process the PR
	conclusion, summary, text, procErr := p.processPR(env.Event.PullRequest)

	// Update the check run
	checkRun, err := p.finishCheckRun(env.Client, env.Owner, env.Repo, checkRun.GetID(), conclusion, summary, text)
	if err != nil {
		return checkRun, err
	}

	// Return failure here too so that the whole suite fails (since the actions
	// suite seems to ignore failing check runs when calculating general failure)
	if procErr != nil {
		return checkRun, fmt.Errorf("failed: %v", procErr)
	}
	return checkRun, nil
}

////////////////////////////////////////////////////////////////////////////////
//                               Check API calls                              //
////////////////////////////////////////////////////////////////////////////////

// createCheckRun creates a new Check-Run.
// It returns an error in case it couldn't be created.
func (p PRPlugin) createCheckRun(client *github.Client, owner, repo, headSHA string) (*github.CheckRun, error) {
	p.debugf("creating check run %q on %s/%s @ %s...", p.Name, owner, repo, headSHA)

	checkRun, res, err := client.Checks.CreateCheckRun(
		context.TODO(),
		owner,
		repo,
		github.CreateCheckRunOptions{
			Name:    p.Name,
			HeadSHA: headSHA,
			Status:  Started.StringP(),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("unable to create check run: %w", err)
	}

	p.debugf("create check API response: %+v", res)
	p.debugf("created run: %+v", checkRun)

	return checkRun, nil
}

// getCheckRun returns the Check-Run, creating it if it doesn't exist.
// It returns an error in case it didn't exist and couldn't be created, or if there are multiple matches.
func (p PRPlugin) getCheckRun(client *github.Client, owner, repo, headSHA string) (*github.CheckRun, error) {
	p.debugf("getting check run %q on %s/%s @ %s...", p.Name, owner, repo, headSHA)

	checkRunList, res, err := client.Checks.ListCheckRunsForRef(
		context.TODO(),
		owner,
		repo,
		headSHA,
		&github.ListCheckRunsOptions{
			CheckName: github.String(p.Name),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("unable to list check runs: %w", err)
	}

	p.debugf("list check API response: %+v", res)
	p.debugf("listed runs: %+v", checkRunList)

	switch n := *checkRunList.Total; {
	case n == 0:
		return p.createCheckRun(client, owner, repo, headSHA)
	case n == 1:
		return checkRunList.CheckRuns[0], nil
	case n > 1:
		return nil, fmt.Errorf("multiple instances of `%s` check run found on %s/%s @ %s",
			p.Name, owner, repo, headSHA)
	default: // Should never happen
		return nil, fmt.Errorf("negative number of instances (%d) of `%s` check run found on %s/%s @ %s",
			n, p.Name, owner, repo, headSHA)
	}
}

// resetCheckRun returns the Check-Run with executing status, creating it if it doesn't exist.
// It returns an error in case it didn't exist and couldn't be created, if there are multiple matches,
// or if it exists but couldn't be updated.
func (p PRPlugin) resetCheckRun(client *github.Client, owner, repo string, headSHA string) (*github.CheckRun, error) {
	checkRun, err := p.getCheckRun(client, owner, repo, headSHA)
	// If it was created we don't need to update it, check its status
	if err != nil || Started.Equal(checkRun.GetStatus()) {
		return checkRun, err
	}

	p.debugf("updating check run %q on %s/%s...", p.Name, owner, repo)

	checkRun, updateResp, err := client.Checks.UpdateCheckRun(
		context.TODO(),
		owner,
		repo,
		checkRun.GetID(),
		github.UpdateCheckRunOptions{
			Name:   p.Name,
			Status: github.String("in-progress"),
		},
	)
	if err != nil {
		return checkRun, fmt.Errorf("unable to reset check run: %w", err)
	}

	p.debugf("update check API response: %+v", updateResp)
	p.debugf("updated run: %+v", checkRun)

	return checkRun, nil
}

// finishCheckRun updates the Check-Run with id checkRunID setting its output.
// It returns an error in case it couldn't be updated.
func (p PRPlugin) finishCheckRun(client *github.Client, owner, repo string, checkRunID int64, conclusion, summary, text string) (*github.CheckRun, error) {
	p.debugf("updating check run %q on %s/%s...", p.Name, owner, repo)

	checkRun, updateResp, err := client.Checks.UpdateCheckRun(context.TODO(), owner, repo, checkRunID, github.UpdateCheckRunOptions{
		Name:        p.Name,
		Conclusion:  github.String(conclusion),
		CompletedAt: &github.Timestamp{Time: time.Now()},
		Output: &github.CheckRunOutput{
			Title:   github.String(p.Title),
			Summary: github.String(summary),
			Text:    github.String(text),
		},
	})
	if err != nil {
		return checkRun, fmt.Errorf("unable to update check run with results: %w", err)
	}

	p.debugf("update check API response: %+v", updateResp)
	p.debugf("updated run: %+v", checkRun)

	return checkRun, nil
}

// duplicateCheckRun creates a new Check-Run with the same info as the provided one but for a new headSHA
func (p PRPlugin) duplicateCheckRun(client *github.Client, owner, repo, headSHA string, checkRun *github.CheckRun) (*github.CheckRun, error) {
	p.debugf("creating check run %q on %s/%s @ %s...", p.Name, owner, repo, headSHA)

	checkRun, res, err := client.Checks.CreateCheckRun(
		context.TODO(),
		owner,
		repo,
		github.CreateCheckRunOptions{
			Name:        p.Name,
			HeadSHA:     headSHA,
			DetailsURL:  checkRun.DetailsURL,
			ExternalID:  checkRun.ExternalID,
			Status:      checkRun.Status,
			Conclusion:  checkRun.Conclusion,
			StartedAt:   checkRun.StartedAt,
			CompletedAt: checkRun.CompletedAt,
			Output:      checkRun.Output,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("unable to create duplicate check run: %w", err)
	}

	p.debugf("create check API response: %+v", res)
	p.debugf("created run: %+v", checkRun)

	return checkRun, nil
}

////////////////////////////////////////////////////////////////////////////////
//                                 Entrypoint                                 //
////////////////////////////////////////////////////////////////////////////////

// entrypoint will call the corresponding handler
func (p PRPlugin) entrypoint(env *ActionsEnv) (err error) {
	switch env.Event.GetAction() {
	case "open":
		err = p.onOpen(env)
	case "reopen":
		err = p.onReopen(env)
	case "edit":
		err = p.onEdit(env)
	case "synchronize":
		err = p.onSync(env)
	default:
		// Do nothing
	}

	return
}

// onOpen handles "open" actions
func (p PRPlugin) onOpen(env *ActionsEnv) error {
	// Create the check run
	checkRun, err := p.createCheckRun(env.Client, env.Owner, env.Repo, env.Event.GetPullRequest().GetHead().GetSHA())
	if err != nil {
		return err
	}

	// Process the PR and submit the results
	_, err = p.processAndSubmit(env, checkRun)
	return err
}

// onReopen handles "reopen" actions
func (p PRPlugin) onReopen(env *ActionsEnv) error {
	// Get the check run
	checkRun, err := p.getCheckRun(env.Client, env.Owner, env.Repo, env.Event.GetPullRequest().GetHead().GetSHA())
	if err != nil {
		return err
	}

	// Rerun the tests if they weren't finished
	if !Finished.Equal(checkRun.GetStatus()) {
		// Process the PR and submit the results
		_, err = p.processAndSubmit(env, checkRun)
		return err
	}

	// Return failure here too so that the whole suite fails (since the actions
	// suite seems to ignore failing check runs when calculating general failure)
	if *checkRun.Conclusion == "failure" {
		return fmt.Errorf("failed: %v", *checkRun.Output.Summary)
	}
	return nil
}

// onEdit handles "edit" actions
func (p PRPlugin) onEdit(env *ActionsEnv) error {
	// Reset the check run
	checkRun, err := p.resetCheckRun(env.Client, env.Owner, env.Repo, env.Event.GetPullRequest().GetHead().GetSHA())
	if err != nil {
		return err
	}

	// Process the PR and submit the results
	_, err = p.processAndSubmit(env, checkRun)
	return err
}

// onSync handles "synchronize" actions
func (p PRPlugin) onSync(env *ActionsEnv) error {
	// Get the check run
	checkRun, err := p.getCheckRun(env.Client, env.Owner, env.Repo, env.Event.GetBefore())
	if err != nil {
		return err
	}

	// Rerun the tests if they weren't finished
	if !Finished.Equal(checkRun.GetStatus()) {
		// Process the PR and submit the results
		checkRun, err = p.processAndSubmit(env, checkRun)
		if err != nil {
			return err
		}
	}

	// Create a duplicate for the new commit
	checkRun, err = p.duplicateCheckRun(env.Client, env.Owner, env.Repo, env.Event.GetAfter(), checkRun)
	if err != nil {
		return err
	}

	// Return failure here too so that the whole suite fails (since the actions
	// suite seems to ignore failing check runs when calculating general failure)
	if *checkRun.Conclusion == "failure" {
		return fmt.Errorf("failed: %v", *checkRun.Output.Summary)
	}
	return nil
}

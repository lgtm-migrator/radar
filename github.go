package radar

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"
	"text/template"
	"time"

	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
)

// Generate and re-use one client per token. Key = token, value = client for token.
var clients = map[string]*github.Client{}

var labels = []string{"radar"}

var bodyTmpl = template.Must(template.New("body").Parse(`{{range .}}- [ ] [{{.GetTitle}}]({{.URL}})
{{end}}`))

func GenerateRadarIssue(radarItemsService RadarItemsService, githubToken string, repo string) (*github.Issue, error) {
	client := getClient(githubToken)

	repoPieces := strings.Split(repo, "/")
	owner, name := repoPieces[0], repoPieces[1]

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	links, err := radarItemsService.List(ctx, -1)
	if err != nil {
		return nil, err
	}

	if issue := getPreviousRadarIssue(ctx, client, owner, name); issue != nil {
		links = append(links, extractGitHubLinks(ctx, client, owner, name, issue)...)
	}

	body, err := joinLinksIntoBody(links)
	if err != nil {
		log.Printf("Couldn't get a radar body: %#v", err)
		return nil, err
	}

	newIssue, _, err := client.Issues.Create(ctx, owner, name, &github.IssueRequest{
		Title:  github.String(getTitle()),
		Body:   github.String(body),
		Labels: &labels,
	})
	if err != nil {
		return nil, err
	}

	return newIssue, nil
}

func getPreviousRadarIssue(ctx context.Context, client *github.Client, owner, name string) *github.Issue {
	query := fmt.Sprintf("repo:%s/%s is:open is:issue label:radar", owner, name)
	opts := &github.SearchOptions{
		Sort:        "created",
		Order:       "desc",
		ListOptions: github.ListOptions{PerPage: 100},
	}
	result, _, err := client.Search.Issues(ctx, query, opts)
	if err != nil {
		log.Printf("Error running query '%s': %#v", query, err)
		return nil
	}

	if len(result.Issues) == 0 {
		log.Printf("No issues for '%s'.", query)
		return nil
	}

	return &result.Issues[0]
}

func getTitle() string {
	return fmt.Sprintf("Radar for %s", time.Now().Format("2006-01-02"))
}

func joinLinksIntoBody(links []RadarItem) (string, error) {
	if len(links) == 0 {
		return "Nothing to do today. Nice work! :sparkles:", nil
	}

	buf := bytes.NewBufferString("A new day! Here's what you have saved:\n\n")
	err := bodyTmpl.Execute(buf, links)
	return buf.String(), err
}

func extractGitHubLinks(ctx context.Context, client *github.Client, owner, name string, issue *github.Issue) []RadarItem {
	var items []RadarItem

	items = append(items, extractLinkedTodosFromMarkdown(issue.GetBody())...)

	opts := &github.IssueListCommentsOptions{
		Sort:        "created",
		Direction:   "asc",
		ListOptions: github.ListOptions{PerPage: 100},
	}
	for {
		comments, resp, err := client.Issues.ListComments(ctx, owner, name, *issue.Number, opts)
		if err != nil {
			log.Printf("Error fetching comments: %#v", err)
			return items
		}

		for _, comment := range comments {
			items = append(items, extractLinkedTodosFromMarkdown(comment.GetBody())...)
		}

		if resp.NextPage == 0 {
			break
		}
		opts.ListOptions.Page = resp.NextPage
	}

	return items
}

func getClient(githubToken string) *github.Client {
	if _, ok := clients[githubToken]; !ok {
		clients[githubToken] = github.NewClient(oauth2.NewClient(
			oauth2.NoContext,
			oauth2.StaticTokenSource(
				&oauth2.Token{AccessToken: githubToken},
			),
		))
	}

	return clients[githubToken]
}

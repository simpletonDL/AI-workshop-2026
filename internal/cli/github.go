package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Defaults of the organization scan of `atlas serve`.
const (
	defaultGitHubAPI   = "https://api.github.com"
	defaultOrgMaxRepos = 100
)

// githubPageSize is the largest page the GitHub API returns.
const githubPageSize = 100

// maxOrgListings bounds how many organization listings are kept.
const maxOrgListings = 100

// maxGitHubResponse bounds the size of one API response.
const maxGitHubResponse = 16 << 20

var (
	errInvalidOrg  = errors.New("invalid organization")
	errOrgNotFound = errors.New("organization not found")
)

// githubLogin is the format of GitHub user and organization names.
var githubLogin = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)

// orgRepo is a repository of an organization as listed by the GitHub API.
type orgRepo struct {
	URL  string `json:"html_url"`
	Size int    `json:"size"` // KiB; 0 for an empty repository
}

// githubOrgs lists the public repositories of GitHub organizations and users
// with as few API calls as possible: one call per 100 repositories, listings
// reused for ttl and then revalidated with their ETag (a 304 answer does not
// count against the rate limit).
type githubOrgs struct {
	api    string // REST API base URL without the trailing slash
	web    string // host of the web URLs that name an organization
	token  string // sent if set: 5000 calls per hour instead of 60
	max    int    // repositories per organization, most recently pushed first
	ttl    time.Duration
	client *http.Client
	now    func() time.Time

	mu       sync.Mutex
	listings map[string]*orgListing // by lowercased name
}

// orgListing is the cached listing of one organization.
type orgListing struct {
	pages     []githubPage
	repos     []orgRepo // at most max
	truncated bool      // the organization has more than max repositories
	fetched   time.Time
}

// githubPage is one page of a listing with its ETag.
type githubPage struct {
	etag  string
	repos []orgRepo
	next  bool
}

// newGitHubOrgs returns a lister for the API at api. The web host of
// organization URLs is the API host without "api." (api.github.com →
// github.com; GitHub Enterprise: https://<host>/api/v3 → <host>).
func newGitHubOrgs(api, token string, max int, ttl time.Duration) (*githubOrgs, error) {
	u, err := url.Parse(strings.TrimRight(api, "/"))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("invalid GitHub API URL %q", api)
	}
	if max < 1 {
		return nil, errors.New("organization repository limit must be at least 1")
	}
	return &githubOrgs{
		api:      u.String(),
		web:      strings.TrimPrefix(strings.ToLower(u.Host), "api."),
		token:    token,
		max:      max,
		ttl:      ttl,
		client:   &http.Client{Timeout: 30 * time.Second},
		now:      time.Now,
		listings: map[string]*orgListing{},
	}, nil
}

// parseOrg returns the organization name of input: a name, or a URL such as
// https://github.com/<name>.
func (g *githubOrgs) parseOrg(input string) (string, error) {
	name := strings.TrimSpace(input)
	if strings.Contains(name, "/") {
		s := name
		if !strings.Contains(s, "://") {
			s = "https://" + s
		}
		u, err := url.Parse(s)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || !strings.EqualFold(u.Host, g.web) {
			return "", fmt.Errorf("%w %q: use a %s organization name or URL", errInvalidOrg, input, g.web)
		}
		name = strings.Trim(u.Path, "/")
	}
	if !githubLogin.MatchString(name) {
		return "", fmt.Errorf("%w %q: use a %s organization name or URL", errInvalidOrg, input, g.web)
	}
	return name, nil
}

// list returns the public repositories of the organization (or user) name,
// most recently pushed first, at most g.max of them; truncated reports that
// there are more. If the API fails, an earlier listing is used.
func (g *githubOrgs) list(ctx context.Context, name string) (repos []orgRepo, truncated bool, err error) {
	key := strings.ToLower(name)
	g.mu.Lock()
	old := g.listings[key]
	g.mu.Unlock()
	if old != nil && g.now().Sub(old.fetched) < g.ttl {
		logFrom(ctx).Info("github: listing cached", "org", name)
		return old.repos, old.truncated, nil
	}

	l, err := g.fetch(ctx, name, old)
	if err != nil {
		if old != nil && !errors.Is(err, errOrgNotFound) {
			logFrom(ctx).Warn("github: listing failed, using an earlier one", "org", name, "error", err.Error())
			return old.repos, old.truncated, nil
		}
		return nil, false, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.listings[key]; !ok && len(g.listings) >= maxOrgListings {
		// Drop the oldest listing.
		var oldest string
		for k, o := range g.listings {
			if oldest == "" || o.fetched.Before(g.listings[oldest].fetched) {
				oldest = k
			}
		}
		delete(g.listings, oldest)
	}
	g.listings[key] = l
	return l.repos, l.truncated, nil
}

// fetch lists the organization page by page, revalidating the pages of old.
func (g *githubOrgs) fetch(ctx context.Context, name string, old *orgListing) (*orgListing, error) {
	perPage := min(g.max, githubPageSize)
	l := &orgListing{fetched: g.now()}
	for n := 1; ; n++ {
		var cached *githubPage
		if old != nil && n <= len(old.pages) {
			cached = &old.pages[n-1]
		}
		page, err := g.fetchPage(ctx, name, n, perPage, cached)
		if err != nil {
			return nil, err
		}
		l.pages = append(l.pages, page)
		l.repos = append(l.repos, page.repos...)
		if len(l.repos) >= g.max || !page.next || len(page.repos) == 0 {
			l.truncated = len(l.repos) > g.max || (len(l.repos) == g.max && page.next)
			break
		}
	}
	if len(l.repos) > g.max {
		l.repos = l.repos[:g.max]
	}
	return l, nil
}

// fetchPage gets page n of the listing; a 304 answer reuses cached.
func (g *githubOrgs) fetchPage(ctx context.Context, name string, n, perPage int, cached *githubPage) (githubPage, error) {
	// /users/<name>/repos lists the public repositories of users and
	// organizations alike.
	u := fmt.Sprintf("%s/users/%s/repos?type=owner&sort=pushed&per_page=%d&page=%d", g.api, url.PathEscape(name), perPage, n)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return githubPage{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "atlas")
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
	if cached != nil && cached.etag != "" {
		req.Header.Set("If-None-Match", cached.etag)
	}
	start := time.Now()
	resp, err := g.client.Do(req)
	if err != nil {
		return githubPage{}, fmt.Errorf("GitHub API: %w", err)
	}
	defer resp.Body.Close()
	logFrom(ctx).Info("github: listing page", "org", name, "page", n, "status", resp.StatusCode,
		"duration", time.Since(start).Round(time.Millisecond), "ratelimit_remaining", resp.Header.Get("X-RateLimit-Remaining"))

	switch {
	case resp.StatusCode == http.StatusNotModified && cached != nil:
		return *cached, nil
	case resp.StatusCode == http.StatusOK:
		page := githubPage{etag: resp.Header.Get("ETag"), next: strings.Contains(resp.Header.Get("Link"), `rel="next"`)}
		if err := json.NewDecoder(io.LimitReader(resp.Body, maxGitHubResponse)).Decode(&page.repos); err != nil {
			return githubPage{}, fmt.Errorf("GitHub API: invalid answer: %w", err)
		}
		return page, nil
	case resp.StatusCode == http.StatusNotFound:
		return githubPage{}, fmt.Errorf("%w: no %s organization or user %q", errOrgNotFound, g.web, name)
	case resp.StatusCode == http.StatusTooManyRequests ||
		(resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0"):
		return githubPage{}, g.rateLimitError(resp)
	default:
		var body struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body)
		if body.Message != "" {
			return githubPage{}, fmt.Errorf("GitHub API: %s: %s", resp.Status, body.Message)
		}
		return githubPage{}, fmt.Errorf("GitHub API: %s", resp.Status)
	}
}

func (g *githubOrgs) rateLimitError(resp *http.Response) error {
	msg := "GitHub API rate limit exceeded"
	if reset, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		msg += fmt.Sprintf(", resets at %s", time.Unix(reset, 0).UTC().Format("15:04 UTC"))
	}
	if g.token == "" {
		msg += "; set GITHUB_TOKEN on the server to raise the limit"
	}
	return errors.New(msg)
}

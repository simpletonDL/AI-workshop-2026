package cli

import (
	_ "embed"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

//go:embed repos.html
var repoFormHTML string

//go:embed repos.js
var reposJS string

//go:embed dancer.html
var dancerHTML string

//go:embed dancer.js
var dancerJS string

// maxRepos is how many repositories a single request (main page or /cluster)
// may scan.
const maxRepos = 10

// pageTemplate parses a page together with the shared repository list form
// and the background dancer.
func pageTemplate(name, html string) *template.Template {
	t := template.Must(template.New(name).Parse(html))
	return template.Must(template.Must(t.Parse(repoFormHTML)).Parse(dancerHTML))
}

// repoSpec is a repository and an optional branch or tag.
type repoSpec struct {
	URL string
	Ref string
}

func (r repoSpec) String() string {
	if r.Ref == "" {
		return r.URL
	}
	return r.URL + " " + r.Ref
}

// readRepoRows reads the repository list of a form: repeated repo and ref
// fields paired by position. Blank rows and duplicates are dropped. It also
// applies the add/remove buttons of a form submitted without JavaScript and
// the edit flag of links that only prefill the form; edit reports that the
// form should be shown again without scanning.
func readRepoRows(q url.Values) (rows []repoSpec, edit bool) {
	remove := -1
	if v, ok := q["remove"]; ok {
		edit = true
		if i, err := strconv.Atoi(v[0]); err == nil {
			remove = i
		}
	}
	refs := q["ref"]
	seen := map[repoSpec]bool{}
	for i, u := range q["repo"] {
		spec := repoSpec{URL: strings.TrimSpace(u)}
		if i < len(refs) {
			spec.Ref = strings.TrimSpace(refs[i])
		}
		if i == remove || spec.URL == "" || seen[spec] {
			continue
		}
		seen[spec] = true
		rows = append(rows, spec)
	}
	if q.Has("add") {
		edit = true
		if len(rows) < maxRepos {
			rows = append(rows, repoSpec{})
		}
	}
	return rows, edit || q.Has("edit")
}

// validateRepos splits rows into repositories to scan and per-repository
// errors for URLs the web UI does not accept.
func validateRepos(rows []repoSpec) (specs []repoSpec, bad []repoResult) {
	for _, spec := range rows {
		if spec.URL == "" {
			continue
		}
		if err := validateRemote(spec.URL); err != nil {
			bad = append(bad, repoResult{Repo: spec, Error: err.Error(), status: http.StatusBadRequest})
			continue
		}
		specs = append(specs, spec)
	}
	return specs, bad
}

// repoQuery returns the query of a page for rows: repo and ref pairs in
// order, refs only if some row has one.
func repoQuery(rows []repoSpec) template.URL {
	withRefs := false
	for _, r := range rows {
		withRefs = withRefs || r.Ref != ""
	}
	var parts []string
	for _, r := range rows {
		if r.URL == "" {
			continue
		}
		parts = append(parts, "repo="+url.QueryEscape(r.URL))
		if withRefs {
			parts = append(parts, "ref="+url.QueryEscape(r.Ref))
		}
	}
	// Built from escaped parts only, so it is safe in an href.
	return template.URL(strings.Join(parts, "&"))
}

// repoFormView is the repository list of a form: at least one row, so there
// is always a field to type into.
type repoFormView struct {
	Rows  []repoSpec
	Max   int
	Focus int // row with autofocus: the first blank one
}

func newRepoFormView(rows []repoSpec) repoFormView {
	if len(rows) == 0 {
		rows = []repoSpec{{}}
	}
	v := repoFormView{Rows: rows, Max: maxRepos}
	for i, r := range rows {
		if r.URL == "" {
			v.Focus = i
			break
		}
	}
	return v
}

func (v repoFormView) CanAdd() bool { return len(v.Rows) < v.Max }

func serveReposJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = w.Write([]byte(reposJS))
}

func serveDancerJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = w.Write([]byte(dancerJS))
}

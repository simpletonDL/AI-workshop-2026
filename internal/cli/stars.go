package cli

import (
	_ "embed"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
)

//go:embed stars.js
var starsJS string

// maxStars is how many starred skills the UI remembers.
const maxStars = 100

// starHeader marks a star request sent by stars.js: it gets the updated
// banana list instead of a redirect.
const starHeader = "X-Atlas-Star"

// starKey identifies a skill: its repository and the path of its SKILL.md.
type starKey struct {
	Repo repoSpec
	Path string
}

// starEntry is a starred skill.
type starEntry struct {
	starKey
	Name string
}

// Query links to the skill: its repository filtered by its name.
func (e starEntry) Query() template.URL {
	// Built from escaped parts only, so it is safe in an href.
	return repoQuery([]repoSpec{e.Repo}) + template.URL("&filter="+url.QueryEscape(e.Name))
}

// stars keeps the starred skills, most recently starred first.
type stars struct {
	mu      sync.Mutex
	entries []starEntry
}

// add stars e, moving it to the top if it is already starred.
func (s *stars) add(e starEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := []starEntry{e}
	for _, old := range s.entries {
		if old.starKey != e.starKey {
			entries = append(entries, old)
		}
	}
	if len(entries) > maxStars {
		entries = entries[:maxStars]
	}
	s.entries = entries
}

func (s *stars) remove(k starKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = slices.DeleteFunc(s.entries, func(e starEntry) bool { return e.starKey == k })
}

func (s *stars) list() []starEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]starEntry(nil), s.entries...)
}

// mark sets Starred on the starred skills of all.
func (s *stars) mark(all []skillView) {
	s.mu.Lock()
	defer s.mu.Unlock()
	starred := map[starKey]bool{}
	for _, e := range s.entries {
		starred[e.starKey] = true
	}
	for i := range all {
		all[i].Starred = starred[starKey{Repo: all[i].Repo, Path: all[i].Path}]
	}
}

// starScan checks that a skill exists before it is starred.
var starScan = scanPlan{log: "star", cloneSteps: 1, discoverSteps: 1}

// serveStar handles POST /star (repo, ref, path, star=1|0, back): it gives a
// skill a banana or takes it back. Starring scans the repository (a cache hit
// right after the page was shown), so only existing skills can be starred and
// their names come from the SKILL.md, not from the visitor.
func serveStar(checkout checkoutFunc, starred *stars) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		key := starKey{
			Repo: repoSpec{URL: strings.TrimSpace(r.PostForm.Get("repo")), Ref: strings.TrimSpace(r.PostForm.Get("ref"))},
			Path: r.PostForm.Get("path"),
		}
		if err := validateRemote(key.Repo.URL); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		log := logFrom(r.Context()).With("repo", redactRepo(key.Repo.URL), "ref", key.Repo.Ref, "path", key.Path)
		if r.PostForm.Get("star") == "1" {
			res := scanRepos(r.Context(), checkout, []repoSpec{key.Repo}, starScan, &progress{})[0]
			if res.Error != "" {
				http.Error(w, res.Error, res.status)
				return
			}
			i := slices.IndexFunc(res.skills, func(s skillView) bool { return s.Path == key.Path })
			if i < 0 {
				http.Error(w, "skill not found: "+key.Path, http.StatusNotFound)
				return
			}
			starred.add(starEntry{starKey: key, Name: res.skills[i].Name})
			log.Info("star: banana given")
		} else {
			starred.remove(key)
			log.Info("star: banana taken back")
		}

		if r.Header.Get(starHeader) != "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_ = servePage.ExecuteTemplate(w, "bananas", starred.list())
			return
		}
		http.Redirect(w, r, localPath(r.PostForm.Get("back")), http.StatusSeeOther)
	}
}

// localPath returns back if it is a path on this server, otherwise "/", so the
// redirect can't send the visitor to another site.
func localPath(back string) string {
	if !strings.HasPrefix(back, "/") || strings.HasPrefix(back, "//") || strings.ContainsAny(back, "\\\r\n") {
		return "/"
	}
	return back
}

func serveStarsJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = w.Write([]byte(starsJS))
}

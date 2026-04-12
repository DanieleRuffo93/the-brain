package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/gomarkdown/markdown"
	"gopkg.in/yaml.v3"
)

var vaultPath = os.Getenv("VAULT_PATH")

// store holds all the documents in memory and it is protected by a RWMutex
// HTTP handlers can read concurrently (RLock), the file system watcher is the only writer (Lock)
var store struct {
	mu   sync.RWMutex
	docs []Doc
}

type Category struct {
	ID    string `yaml:"id"    json:"id"`
	Label string `yaml:"label"    json:"label"`
}

var categories []Category

const uncategorizedID = "uncategorized"

type Frontmatter struct {
	Title    string
	Category string
	Tags     []string
	Aliases  []string
	Related  []string
}

func (f Frontmatter) Print() {
	fmt.Printf("Frontmatter:\n\tCategory:%s\n\tTitle: %s\n\tTags: %s\n\tAliases: %s\n\tRelated: %s\n",
		f.Title,
		f.Category,
		strings.Join(f.Tags, ","),
		strings.Join(f.Aliases, ","),
		strings.Join(f.Related, ","))
}

type Doc struct {
	Slug        string
	Frontmatter Frontmatter
	Content     string
	HTML        string
}

type DocSummary struct {
	Slug     string   `json:"slug"`
	Title    string   `json:"title"`
	Category string   `json:"category"`
	Tags     []string `json:"tags"`
}

func getSlugFromPath(vaultPath, filePath string) string {
	rel, _ := filepath.Rel(vaultPath, filePath)
	rel = strings.TrimSuffix(rel, ".md")
	return strings.ReplaceAll(rel, string(filepath.Separator), "/")
}

func parseArray(str string) []string {
	if str == "" {
		return nil
	}

	if len(str) >= 2 && str[0] == '[' && str[len(str)-1] == ']' {
		str = str[1 : len(str)-1]
	}

	parts := strings.Split(str, ",")

	var result []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}

	return result
}

func parseFrontmatter(block string) Frontmatter {
	var result Frontmatter
	for line := range strings.SplitSeq(block, "\n") {
		line = strings.TrimSpace(line)
		part := strings.SplitN(line, ":", 2)
		if len(part) != 2 {
			continue
		}

		key := strings.TrimSpace(part[0])
		value := strings.TrimSpace(part[1])
		switch key {
		case "title":
			result.Title = value
		case "category":
			result.Category = value
		case "tags":
			result.Tags = parseArray(value)
		case "aliases":
			result.Aliases = parseArray(value)
		case "related":
			result.Related = parseArray(value)
		}
	}
	return result
}

func splitData(data string) (string, string) {
	lines := strings.Split(data, "\n")

	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", data
	}

	var fmLines []string
	var i = 1
	for ; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "---" {
			i++
			break
		}
		fmLines = append(fmLines, lines[i])
	}

	return strings.Join(fmLines, "\n"), strings.Join(lines[i:], "\n")
}

func resolveTitle(title string) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(title))
	for _, doc := range store.docs {
		if strings.ToLower(doc.Frontmatter.Title) == lower {
			return doc.Slug, true
		}
		for _, alias := range doc.Frontmatter.Aliases {
			if strings.ToLower(alias) == lower {
				return doc.Slug, true
			}
		}
	}
	return "", false
}

var wikiLinkRe = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

func processWikiLinks(content string) string {
	return wikiLinkRe.ReplaceAllStringFunc(content, func(match string) string {
		title := match[2 : len(match)-2]
		slug, ok := resolveTitle(title)
		if ok {
			return fmt.Sprintf(`<a class="wiki-link" data-slug="%s" href="#">%s</a>`, slug, title)
		}
		return fmt.Sprintf(`<span class="wiki-link-broken">%s</span>`, title)
	})
}

func loadCategories(vault string) []Category {
	path := filepath.Join(vault, "categories.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("warn: could not read categories %v", err)
		}
		return nil
	}

	var cfg struct {
		Categories []Category `yaml:"categories"`
	}
	err = yaml.Unmarshal(data, &cfg)
	if err != nil {
		log.Printf("warn: could not parse categories.yaml: %v", err)
		return nil
	}
	log.Printf("done: %d categories loaded", len(cfg.Categories))
	return cfg.Categories
}

func resolveCategory(id string) string {
	if id == "" {
		return uncategorizedID
	}

	for _, category := range categories {
		if category.ID == id {
			return id
		}
	}

	log.Printf("warn: unknown category %q — falling back to uncategorized", id)
	return uncategorizedID
}

func parseDoc(path string) (Doc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Doc{}, err
	}

	fmBlock, content := splitData(string(data))
	frontmatter := parseFrontmatter(fmBlock)
	html := markdown.ToHTML([]byte(content), nil, nil)
	return Doc{
		Slug:        getSlugFromPath(vaultPath, path),
		Frontmatter: frontmatter,
		Content:     content,
		HTML:        string(html),
	}, nil
}

func loadDocs(vault string) []Doc {
	var docs []Doc

	filepath.WalkDir(vault, func(path string, d os.DirEntry, err error) error {

		if err != nil {
			log.Printf("warn: cannot access %s: %v", path, err)
			return nil
		}

		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}

		doc, err := parseDoc(path)
		if err != nil {
			log.Printf("warn: cannot parse %s: %v", path, err)
			return nil
		}

		docs = append(docs, doc)
		return nil
	})

	return docs
}

// watchVault starts goroutine that listens for filesystem events on the vault directory and keeps store.docs in sync
func watchVault(vault string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("warn: could not start the vault watcher: %v", err)
		return
	}

	// fsnotify does not recurse automatically in subfolders. Need to add them manually
	filepath.WalkDir(vaultPath, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		return watcher.Add(path)
	})

	go func() {
		defer watcher.Close()
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				handleFsEvent(event, vault, watcher)

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("watcher error: %v", err)
			}
		}
	}()
}

func handleFsEvent(event fsnotify.Event, vault string, watcher *fsnotify.Watcher) {
	path := event.Name

	// In case a new directory has been created, add it to the watcher to track .md files inside
	if event.Has(fsnotify.Create) {
		info, err := os.Stat(path)
		if err == nil && info.IsDir() {
			watcher.Add(path)
			log.Printf("watcher: added new directory: %s", path)
			return
		}
	}

	if filepath.Base(path) == "categories.yaml" && (event.Has(fsnotify.Create) || event.Has(fsnotify.Write)) {
		store.mu.Lock()
		categories = loadCategories(vault)
		store.mu.Unlock()
		log.Printf("watcher: reloaded categories.yaml")
		return
	}

	if !strings.HasSuffix(path, ".md") {
		return
	}

	switch {
	case event.Has(fsnotify.Create) || event.Has(fsnotify.Write):
		doc, err := parseDoc(path)
		if err != nil {
			log.Printf("watcher: failed to parse %s: %v", path, err)
			return
		}
		upsertDoc(doc)
		log.Printf("watcher: upserted %s", doc.Slug)
	case event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename):
		slug := getSlugFromPath(vault, path)
		removeDoc(slug)
		log.Printf("watcher: removed %s", slug)
	}
}

func upsertDoc(doc Doc) {
	store.mu.Lock()
	defer store.mu.Unlock()

	for i, d := range store.docs {
		if d.Slug == doc.Slug {
			store.docs[i] = doc
			return
		}
	}
	store.docs = append(store.docs, doc)
}

func removeDoc(slug string) {
	store.mu.Lock()
	defer store.mu.Unlock()

	for i, d := range store.docs {
		if d.Slug == slug {
			store.docs = append(store.docs[:i], store.docs[i+1:]...)
			return
		}
	}
}

func writeJson(w http.ResponseWriter, data any) {
	w.Header().Set("content-type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func handleDocs(w http.ResponseWriter, r *http.Request) {
	store.mu.RLock()
	defer store.mu.RUnlock()

	var result []DocSummary
	for _, doc := range store.docs {
		result = append(result, DocSummary{
			Slug:     doc.Slug,
			Title:    doc.Frontmatter.Title,
			Category: doc.Frontmatter.Category,
			Tags:     doc.Frontmatter.Tags,
		})
	}
	writeJson(w, result)
}

func handleDoc(w http.ResponseWriter, r *http.Request) {
	store.mu.RLock()
	defer store.mu.RUnlock()

	slug := r.PathValue("slug")
	for _, doc := range store.docs {
		if doc.Slug == slug {
			html := processWikiLinks(doc.Content)
			html = string(markdown.ToHTML([]byte(html), nil, nil))

			type relatedDoc struct {
				Slug  string `json:"slug"`
				Title string `json:"title"`
			}
			related := make([]relatedDoc, 0, len(doc.Frontmatter.Related))
			for _, title := range doc.Frontmatter.Related {
				resolvedSlug, ok := resolveTitle(title)
				if ok {
					related = append(related, relatedDoc{
						Slug:  resolvedSlug,
						Title: title,
					})
				}
			}
			writeJson(w, struct {
				Slug    string       `json:"slug"`
				Title   string       `json:"title"`
				Tags    []string     `json:"tags"`
				Related []relatedDoc `json:"related"`
				HTML    string       `json:"html"`
			}{
				Slug:    doc.Slug,
				Title:   doc.Frontmatter.Title,
				Tags:    doc.Frontmatter.Tags,
				Related: related,
				HTML:    html,
			})
			return
		}
	}
	http.NotFound(w, r)
}

func handleCategories(w http.ResponseWriter, r *http.Request) {
	store.mu.RLock()
	defer store.mu.RUnlock()

	type tagCount struct {
		Tag   string `json:"tag"`
		Count int    `json:"count"`
	}
	type categoryResponse struct {
		ID    string     `json:"id"`
		Label string     `json:"label"`
		Count int        `json:"count"`
		Tags  []tagCount `json:"tags"`
	}

	docCounts := map[string]int{}
	tagCounts := map[string]map[string]int{} // category → tag → count

	for _, doc := range store.docs {
		catID := resolveCategory(doc.Frontmatter.Category)
		docCounts[catID]++
		if tagCounts[catID] == nil {
			tagCounts[catID] = map[string]int{}
		}
		for _, tag := range doc.Frontmatter.Tags {
			tagCounts[catID][tag]++
		}
	}

	buildTags := func(catID string) []tagCount {
		var tags []tagCount
		for tag, count := range tagCounts[catID] {
			tags = append(tags, tagCount{tag, count})
		}
		// Sort by count descending, then alphabetically for stability.
		sort.Slice(tags, func(i, j int) bool {
			if tags[i].Count != tags[j].Count {
				return tags[i].Count > tags[j].Count
			}
			return tags[i].Tag < tags[j].Tag
		})
		return tags
	}

	result := make([]categoryResponse, 0, len(categories)+1)
	for _, c := range categories {
		result = append(result, categoryResponse{
			ID:    c.ID,
			Label: c.Label,
			Count: docCounts[c.ID],
			Tags:  buildTags(c.ID),
		})
	}

	if docCounts[uncategorizedID] > 0 {
		result = append(result, categoryResponse{
			ID:    uncategorizedID,
			Label: "Uncategorized",
			Count: docCounts[uncategorizedID],
			Tags:  buildTags(uncategorizedID),
		})
	}

	writeJson(w, result)
}

func handleCategory(w http.ResponseWriter, r *http.Request) {
	catID := r.PathValue("id")

	store.mu.RLock()
	defer store.mu.RUnlock()

	var result []DocSummary
	for _, doc := range store.docs {
		if resolveCategory(doc.Frontmatter.Category) == catID {
			result = append(result, DocSummary{
				Slug:     doc.Slug,
				Title:    doc.Frontmatter.Title,
				Category: catID,
				Tags:     doc.Frontmatter.Tags,
			})
		}
	}
	writeJson(w, result)
}

func handleTag(w http.ResponseWriter, r *http.Request) {
	tag := r.PathValue("tag")
	var result []DocSummary
	for _, doc := range store.docs {
		for _, t := range doc.Frontmatter.Tags {
			if t == tag {
				result = append(result, DocSummary{doc.Slug, doc.Frontmatter.Title, doc.Frontmatter.Category, doc.Frontmatter.Tags})
			}
		}
	}
	writeJson(w, result)
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	store.mu.RLock()
	defer store.mu.RUnlock()

	query := strings.ToLower(r.URL.Query().Get("q"))
	var result []DocSummary
	for _, doc := range store.docs {
		if strings.Contains(strings.ToLower(doc.Frontmatter.Title), query) ||
			strings.Contains(strings.ToLower(doc.Content), query) {
			result = append(result, DocSummary{doc.Slug, doc.Frontmatter.Title, doc.Frontmatter.Category, doc.Frontmatter.Tags})
		}
	}
	writeJson(w, result)
}

func main() {

	if vaultPath == "" {
		log.Fatal("VAULT_PATH is not set")
	}

	categories = loadCategories(vaultPath)
	store.docs = loadDocs(vaultPath)
	log.Printf("\ndone: %d files loaded\n", len(store.docs))

	watchVault(vaultPath)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/docs", handleDocs)
	mux.HandleFunc("GET /api/doc/{slug...}", handleDoc)
	mux.HandleFunc("GET /api/categories", handleCategories)
	mux.HandleFunc("GET /api/category/{id}", handleCategory)
	mux.HandleFunc("GET /api/tag/{tag}", handleTag)
	mux.HandleFunc("GET /api/search", handleSearch)

	mux.Handle("/", http.FileServer(http.Dir("public")))

	log.Println("Server listening on port 8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}

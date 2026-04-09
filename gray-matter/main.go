package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gomarkdown/markdown"
)

var vaultPath = os.Getenv("VAULT_PATH")
var docs []Doc

type Frontmatter struct {
	Title   string
	Tags    []string
	Aliases []string
	Related []string
}

func (f Frontmatter) Print() {
	fmt.Printf("Frontmatter:\n\tTitle: %s\n\tTags: %s\n\tAliases: %s\n\tRelated: %s\n",
		f.Title,
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
	Slug  string   `json:"slug"`
	Title string   `json:"title"`
	Tags  []string `json:"tags"`
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

		data, err := os.ReadFile(path)

		if err != nil {
			log.Printf("warn: cannot access %s: %v", path, err)
			return nil
		}

		fmBlock, content := splitData(string(data))
		frontmatter := parseFrontmatter(fmBlock)
		html := markdown.ToHTML([]byte(content), nil, nil)

		docs = append(docs, Doc{
			Slug:        getSlugFromPath(vault, path),
			Frontmatter: frontmatter,
			Content:     content,
			HTML:        string(html),
		})

		return nil
	})

	return docs
}

func writeJson(w http.ResponseWriter, data any) {
	w.Header().Set("content-type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func handleDocs(w http.ResponseWriter, r *http.Request) {
	var result []DocSummary
	for _, doc := range docs {
		result = append(result, DocSummary{doc.Slug, doc.Frontmatter.Title, doc.Frontmatter.Tags})
	}
	writeJson(w, result)
}

func handleDoc(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	for _, doc := range docs {
		if doc.Slug == slug {
			writeJson(w, struct {
				Slug    string   `json:"slug"`
				Title   string   `json:"title"`
				Tags    []string `json:"tags"`
				Related []string `json:"related"`
				HTML    string   `json:"html"`
			}{
				Slug:    doc.Slug,
				Title:   doc.Frontmatter.Title,
				Tags:    doc.Frontmatter.Tags,
				Related: doc.Frontmatter.Related,
				HTML:    doc.HTML,
			})
			return
		}
	}
	http.NotFound(w, r)
}

func handleTags(w http.ResponseWriter, r *http.Request) {
	type tagCount struct {
		Tag   string `json:"tag"`
		Count int    `json:"count"`
	}
	counts := map[string]int{}
	for _, doc := range docs {
		for _, tag := range doc.Frontmatter.Tags {
			counts[tag]++
		}
	}
	var result []tagCount
	for tag, count := range counts {
		result = append(result, tagCount{tag, count})
	}
	writeJson(w, result)
}

func handleTag(w http.ResponseWriter, r *http.Request) {
	tag := r.PathValue("tag")
	var result []DocSummary
	for _, doc := range docs {
		for _, t := range doc.Frontmatter.Tags {
			if t == tag {
				result = append(result, DocSummary{doc.Slug, doc.Frontmatter.Title, doc.Frontmatter.Tags})
			}
		}
	}
	writeJson(w, result)
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.ToLower(r.URL.Query().Get("q"))
	var result []DocSummary
	for _, doc := range docs {
		if strings.Contains(strings.ToLower(doc.Frontmatter.Title), query) ||
			strings.Contains(strings.ToLower(doc.Content), query) {
			result = append(result, DocSummary{doc.Slug, doc.Frontmatter.Title, doc.Frontmatter.Tags})
		}
	}
	writeJson(w, result)
}

func main() {

	if vaultPath == "" {
		log.Fatal("VAULT_PATH is not set")
	}

	docs = loadDocs(vaultPath)
	log.Printf("\ndone: %d files loaded\n", len(docs))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/docs", handleDocs)
	mux.HandleFunc("GET /api/doc/{slug...}", handleDoc)
	mux.HandleFunc("GET /api/tags", handleTags)
	mux.HandleFunc("GET /api/tag/{tag}", handleTag)
	mux.HandleFunc("GET /api/search", handleSearch)

	mux.Handle("/", http.FileServer(http.Dir("public")))

	log.Println("Server listening on port 8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}

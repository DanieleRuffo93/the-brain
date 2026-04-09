package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/gomarkdown/markdown"
)

var vaultPath = os.Getenv("VAULT_PATH")

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

		if d.IsDir() && !strings.HasSuffix(d.Name(), ".md") {
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

func main() {

	if vaultPath == "" {
		log.Fatal("VAULT_PATH is not set")
	}

	docs := loadDocs(vaultPath)
	log.Printf("\ndone: %d files loaded\n", len(docs))
}

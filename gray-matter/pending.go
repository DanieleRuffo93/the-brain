package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

var pendingPath string // set in main() as filepath.Join(vaultPath, "pending")

func titleToSlug(title string) string {
	slug := strings.ToLower(title)
	slug = strings.ReplaceAll(slug, " ", "-")

	var result strings.Builder
	for _, r := range slug {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			result.WriteRune(r)
		}
	}

	// collapse multiple dashes
	s := result.String()
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

func savePending(content string) (string, error) {
	if err := os.MkdirAll(pendingPath, 0755); err != nil {
		return "", fmt.Errorf("could not create pending dir: %w", err)
	}

	fmBlock, _ := splitData(content)
	fm := parseFrontmatter(fmBlock)

	slug := titleToSlug(fm.Title)
	if slug == "" {
		return "", fmt.Errorf("could not derive slug from title %q", fm.Title)
	}

	destPath := filepath.Join(pendingPath, slug+".md")
	if err := os.WriteFile(destPath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("could not write pending file: %w", err)
	}

	return slug, nil
}

func pendingSlugToPath(slug string) string {
	return filepath.Join(pendingPath, slug+".md")
}

// handleDraft handles POST /api/draft
func handleDraft(w http.ResponseWriter, r *http.Request) {
	var req DraftRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Topic) == "" {
		http.Error(w, "topic is required", http.StatusBadRequest)
		return
	}

	draft, err := generateDraft(req)
	if err != nil {
		log.Printf("draft generation failed: %v", err)
		http.Error(w, "generation failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	slug, err := savePending(draft)
	if err != nil {
		log.Printf("failed to save pending doc: %v", err)
		http.Error(w, "failed to save draft", http.StatusInternalServerError)
		return
	}

	fmBlock, _ := splitData(draft)
	fm := parseFrontmatter(fmBlock)

	writeJson(w, map[string]any{
		"slug":         "pending/" + slug,
		"title":        fm.Title,
		"pending":      true,
		"new_category": strings.HasPrefix(fm.Category, "__NEW__"),
	})
}

// handlePending handles GET /api/pending
func handlePending(w http.ResponseWriter, r *http.Request) {
	store.mu.RLock()
	defer store.mu.RUnlock()

	type pendingDoc struct {
		Slug        string   `json:"slug"`
		Title       string   `json:"title"`
		Category    string   `json:"category"`
		Tags        []string `json:"tags"`
		Summary     string   `json:"summary,omitempty"`
		NewCategory bool     `json:"new_category"`
	}

	var result []pendingDoc
	for _, doc := range store.docs {
		if !doc.Pending {
			continue
		}
		result = append(result, pendingDoc{
			Slug:        doc.Slug,
			Title:       doc.Frontmatter.Title,
			Category:    doc.Frontmatter.Category,
			Tags:        doc.Frontmatter.Tags,
			Summary:     extractSummary(doc.Content),
			NewCategory: strings.HasPrefix(doc.Frontmatter.Category, "__NEW__"),
		})
	}

	if result == nil {
		result = []pendingDoc{}
	}
	writeJson(w, result)
}

// handleApprove handles POST /api/review/:slug/approve
func handleApprove(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	store.mu.RLock()
	var targetDoc *Doc
	for i := range store.docs {
		if store.docs[i].Slug == "pending/"+slug {
			targetDoc = &store.docs[i]
			break
		}
	}
	store.mu.RUnlock()

	if targetDoc == nil {
		http.NotFound(w, r)
		return
	}

	srcPath := pendingSlugToPath(slug)
	destPath := filepath.Join(vaultPath, slug+".md")

	// Handle new category: requires explicit confirmation from the frontend
	if strings.HasPrefix(targetDoc.Frontmatter.Category, "__NEW__:") {
		var body struct {
			ConfirmNewCategory bool   `json:"confirm_new_category"`
			CategoryLabel      string `json:"category_label,omitempty"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		if !body.ConfirmNewCategory {
			w.WriteHeader(http.StatusConflict)
			writeJson(w, map[string]any{
				"error":        "new_category_confirmation_required",
				"suggested_id": strings.TrimPrefix(targetDoc.Frontmatter.Category, "__NEW__:"),
			})
			return
		}

		newCatID := strings.TrimPrefix(targetDoc.Frontmatter.Category, "__NEW__:")
		newCatLabel := body.CategoryLabel
		if newCatLabel == "" {
			// Derive label from id: "cognitive-science" → "Cognitive Science"
			words := strings.Split(newCatID, "-")
			for i, w := range words {
				if len(w) > 0 {
					words[i] = strings.ToUpper(w[:1]) + w[1:]
				}
			}
			newCatLabel = strings.Join(words, " ")
		}

		if err := appendCategory(newCatID, newCatLabel); err != nil {
			log.Printf("failed to append category: %v", err)
			http.Error(w, "failed to update categories.yaml", http.StatusInternalServerError)
			return
		}

		// Read raw file from disk — targetDoc.Content does not include the frontmatter
		raw, err := os.ReadFile(srcPath)
		if err != nil {
			http.Error(w, "failed to read pending file", http.StatusInternalServerError)
			return
		}

		// Fix the __NEW__ category in the raw content before writing to vault
		fixed := strings.ReplaceAll(string(raw), "category: __NEW__:"+newCatID, "category: "+newCatID)

		if err := os.WriteFile(destPath, []byte(fixed), 0644); err != nil {
			http.Error(w, "failed to write approved file", http.StatusInternalServerError)
			return
		}
		if err := os.Remove(srcPath); err != nil {
			log.Printf("warn: could not remove pending file %s: %v", srcPath, err)
		}

		log.Printf("approved (new category %s): %s → %s", newCatID, srcPath, destPath)
		writeJson(w, map[string]any{"approved": true, "slug": slug})
		return
	}

	// Standard approve — move file directly
	if err := os.Rename(srcPath, destPath); err != nil {
		log.Printf("failed to move pending doc %s: %v", slug, err)
		http.Error(w, "failed to approve document", http.StatusInternalServerError)
		return
	}

	log.Printf("approved: %s → %s", srcPath, destPath)
	writeJson(w, map[string]any{"approved": true, "slug": slug})
}

// handleReject handles POST /api/review/:slug/reject
func handleReject(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	path := pendingSlugToPath(slug)

	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "failed to reject document", http.StatusInternalServerError)
		return
	}

	log.Printf("rejected: %s", path)
	writeJson(w, map[string]any{"rejected": true, "slug": slug})
}

// appendCategory adds a new category entry to categories.yaml
func appendCategory(id, label string) error {
	catPath := filepath.Join(vaultPath, "categories.yaml")

	data, err := os.ReadFile(catPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	var cfg struct {
		Categories []Category `yaml:"categories"`
	}
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return err
		}
	}

	// avoid duplicates
	for _, c := range cfg.Categories {
		if c.ID == id {
			return nil
		}
	}

	cfg.Categories = append(cfg.Categories, Category{ID: id, Label: label})

	out, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	if err := os.WriteFile(catPath, out, 0644); err != nil {
		return err
	}

	// update in-memory categories immediately — fsnotify will also catch the file change
	store.mu.Lock()
	categories = append(categories, Category{ID: id, Label: label})
	store.mu.Unlock()

	return nil
}

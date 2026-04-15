package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

var anthropicAPIKey = os.Getenv("ANTHROPIC_API_KEY")

// const anthropicModel = "claude-haiku-4-5"
// const anthropicModel = "claude-opus-4-6"
const anthropicModel = "claude-sonnet-4-6"

const anthropicMaxTokens = 4096

type DraftRequest struct {
	Topic    string `json:"topic"`
	URL      string `json:"url,omitempty"`
	URLTitle string `json:"url_title,omitempty"`
	Language string `json:"language,omitempty"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func buildVaultContext() string {
	store.mu.RLock()
	defer store.mu.RUnlock()

	var sb strings.Builder
	for _, doc := range store.docs {
		if doc.Pending {
			continue
		}
		if doc.Frontmatter.Title == "" {
			continue
		}
		sb.WriteString("- ")
		sb.WriteString(doc.Frontmatter.Title)
		if len(doc.Frontmatter.Tags) > 0 {
			sb.WriteString(" [")
			sb.WriteString(strings.Join(doc.Frontmatter.Tags, ", "))
			sb.WriteString("]")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func buildCategoriesContext() string {
	var sb strings.Builder
	for _, c := range categories {
		sb.WriteString("- ")
		sb.WriteString(c.ID)
		sb.WriteString(": ")
		if c.Description != "" {
			sb.WriteString(c.Description)
		} else {
			sb.WriteString(c.Label)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func buildSystemPrompt(lang, categoriesCtx, docsCtx string) string {
	if lang == "" {
		lang = "English"
	}

	return fmt.Sprintf(`You are a knowledge base writer for a personal PKM system called "The Brain".
Your task is to produce a single, self-contained Markdown document about the requested topic.

ABSOLUTE RULES:
- Output ONLY the raw Markdown document. No preamble, no explanation, no comments, no code fences around the output.
- The document must be written in: %s
- Every section title must also be in %s
- Be exhaustive and precise. This document must stand alone as a complete explanation of the concept. Do not be superficial.
- Avoid using hyphen
- The blockquote (defined later) must be a single concise sentence, maximum 120 characters.

FRONTMATTER:
The document must open with a YAML frontmatter block in this exact format:

---
title: <A title that best fit the topic of this document>
category: <see CATEGORIES below>
tags: [<2-5 lowercase tags, comma separated>]
aliases: [<alternative names or spellings, can be empty>]
related: [<titles from EXISTING DOCUMENTS that are genuinely connected>]
---

CATEGORY RULES:
- Choose the category id that best fits from this list:
%s
- Categories represent broad knowledge domains. Prefer existing categories over creating new ones.
  Only suggest a new category if the topic is genuinely outside every existing domain.
  When in doubt, use the closest existing category.
- If truly no category fits, write: category: __NEW__:<your-suggested-id>
- Use only lowercase-hyphenated ids (e.g. cognitive-science, not "Cognitive Science")

RELATED RULES:
- Only use titles that appear verbatim in the EXISTING DOCUMENTS list below.
- Do not invent titles. If nothing is genuinely related, leave the list empty.
- Use the exact title string as it appears in the list.

EXISTING DOCUMENTS:
%s
DOCUMENT STRUCTURE:
After the frontmatter, the document must contain these elements in order:

1. A blockquote immediately after the frontmatter — a single dense sentence defining the concept:
> <definition>

2. A short section (2-4 sentences) explaining what this is in plain terms, as if to someone with no prior context.

3. One or more substantive sections that explain the concept thoroughly. Structure these sections however best serves the topic — use analogies, comparisons, historical context, step-by-step breakdowns, ASCII diagrams, whatever makes the concept genuinely clear. There is no fixed list of required section titles here. Use your judgment.

4. A concrete example or illustration. For technical topics this is usually code. For non-technical topics this might be a real-world scenario, a worked example, or a case study. Always include something concrete.

5. A section on where this concept appears in the real world — real tools, systems, disciplines, or situations where someone would encounter it.

6. A "Connected concepts" section listing related concepts as wiki links:
[[Title One]], [[Title Two]]
Only link to concepts that genuinely connect. Wiki links here do not need to match EXISTING DOCUMENTS — they can point to concepts not yet in the vault.

7. A resources section with 2-4 references. Format:
- [Title](url)`,
		lang, lang, categoriesCtx, docsCtx)
}

func buildUserMessage(req DraftRequest) string {
	var sb strings.Builder
	sb.WriteString("TOPIC: ")
	sb.WriteString(req.Topic)

	if req.URL != "" {
		sb.WriteString("\n\nREFERENCE URL: ")
		sb.WriteString(req.URL)
		if req.URLTitle != "" {
			sb.WriteString("\nREFERENCE TITLE: ")
			sb.WriteString(req.URLTitle)
		}
		sb.WriteString("\nInclude this as the first entry in the Resources section.")
	}

	return sb.String()
}

func languageLabel(code string) string {
	switch strings.ToLower(code) {
	case "it":
		return "Italian"
	case "en", "":
		return "English"
	default:
		return code
	}
}

func generateDraft(req DraftRequest) (string, error) {
	if anthropicAPIKey == "" {
		return "", fmt.Errorf("ANTHROPIC_API_KEY is not set")
	}

	lang := languageLabel(req.Language)
	categoriesCtx := buildCategoriesContext()
	docsCtx := buildVaultContext()

	systemPrompt := buildSystemPrompt(lang, categoriesCtx, docsCtx)
	userMessage := buildUserMessage(req)

	payload := anthropicRequest{
		Model:     anthropicModel,
		MaxTokens: anthropicMaxTokens,
		System:    systemPrompt,
		Messages: []anthropicMessage{
			{Role: "user", Content: userMessage},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", anthropicAPIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	var anthropicResp anthropicResponse
	if err := json.Unmarshal(respBody, &anthropicResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if anthropicResp.Error != nil {
		return "", fmt.Errorf("Anthropic API error: %s", anthropicResp.Error.Message)
	}

	for _, block := range anthropicResp.Content {
		if block.Type == "text" {
			return strings.TrimSpace(block.Text), nil
		}
	}

	return "", fmt.Errorf("no text content in response")
}

# The Brain

**The Brain** is a Personal Knowledge Management (PKM), a self-hosted vault of Markdown documents.

**Status:** WIP


## Why I built this

Way too often I realize that a concept needs to be refined, deepened or is missing entirely in my head. In that instant the problem is that I rarely have the immediate time or the right tools at hand to write a structured note from scratch. I sometimes write the topic somewhere and I'd tell myself I'd get to it later, and forget.

I wanted to remove that friction entirely. The Brain lets me capture a topic in seconds by just naming it. Claude drafts a structured concept document, I review it, and decide whether it earns a place in my vault. The "AI proposes, user disposes" model means I stay in control of what actually gets stored, without the overhead of writing it myself from scratch.

If you feel that this project could fit your workflow, enjoy.


## Roadmap

The next step is taking this further into my own development workflow: a **TUI client**, built with Bubble Tea, that lives inside Neovim and lets me consult my vault and draft new concept documents without ever leaving the terminal. It will be my first TUI project, and it will talk directly to this API.

I also plan to integrate to lavarege this project to study and learn something about embedding. I will implement a **RAG** leveraging Qdrant probably.


## Architecture Overview

```
the-brain/
├── cortex/               # The vault - Markdown documents live here
│   ├── categories.yaml   # Category definitions
│   └── pending/          # AI-generated drafts awaiting review
└── gray-matter/          # Go backend + SPA frontend
    ├── main.go
    ├── ai.go
    ├── pending.go
    ├── public/
    ├── Dockerfile
    └── go.mod
```

The Go server watches the vault directory with `fsnotify`. The frontend is a vanilla SPA served as a static file.


## Features

- **Markdown vault** with YAML frontmatter (`title`, `category`, `tags`, `aliases`, `related`)
- **AI drafting pipeline** — generate a document from a topic or URL via Claude; draft lands in `pending/` for review
- **Approve / reject workflow** — review pending docs in the UI before they enter the vault
- **New category detection** — if the AI proposes a category that doesn't exist yet, you're prompted to confirm before it's added to `categories.yaml`
- **Wiki links** — `[[Title]]` syntax resolves to internal links
- **Full-text search** across titles and content
- **Category & tag navigation** in the sidebar
- **In-browser editor** — edit any document's raw Markdown with unsaved-changes protection
- **fsnotify file watching** — edits made outside the app (e.g. in Obsidian) are picked up automatically


## Document Format

Every document is a Markdown file with this structure:

```markdown
---
title: Pratt Parsing
category: computer-science
tags: [parsing, compilers, expressions, recursive-descent]
aliases: [Top Down Operator Precedence]
related: [Abstract Syntax Tree, Lexer]
---

> A single-sentence definition of the concept.

Short plain-language explanation (2-4 sentences).

## Sections
One or more substantive sections that explain the concept thoroughly. The AI will decide how to structure these sections to best serves the topic using analogies, comparisons, historical context, step-by-step breakdowns, ASCII diagrams, whatever makes the concept genuinely clear.

## Examples
A concrete example or illustration. For technical topics this is usually code. For non-technical topics this might be a real-world scenario, a worked example, or a case study.

## Real Usage
A section on where this concept appears in the real world, real tools, systems, disciplines, or situations where someone would encounter it.

## Connected Concepts
Connected concepts section listing related concepts as wiki links.

## Resources
A resources section with 2-4 references.
```

## REST API

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/docs` | All documents (summaries) |
| `GET` | `/api/doc/:slug` | Single document with rendered HTML |
| `GET` | `/api/doc/raw/:slug` | Raw Markdown source |
| `PUT` | `/api/doc/:slug` | Update document content |
| `GET` | `/api/categories` | Categories with doc counts and tag breakdown |
| `GET` | `/api/category/:id` | Documents in a category |
| `GET` | `/api/tag/:tag` | Documents with a tag |
| `GET` | `/api/search?q=` | Full-text search |
| `POST` | `/api/draft` | Generate an AI draft |
| `GET` | `/api/pending` | List pending drafts |
| `POST` | `/api/review/:slug/approve` | Approve a pending draft |
| `POST` | `/api/review/:slug/reject` | Reject and delete a pending draft |



## Running with Docker

Copy `.env.example` to `.env` and fill in your values:

```env
ANTHROPIC_API_KEY=sk-ant-...
HOST_PORT=8080
```

Then:

```bash
docker compose up --build
```

The vault is mounted at `./cortex` inside the container as `/cortex`. Documents you add or edit on disk are picked up automatically.


## Running locally (without Docker)

```bash
cd gray-matter
VAULT_PATH=../cortex ANTHROPIC_API_KEY=sk-ant-... go run .
```

The server starts on `:8080`.


## Categories

Categories are defined in `cortex/categories.yaml`:

```yaml
categories:
  - id: computer-science
    label: Computer Science
    description: Programming languages, compilers, algorithms, ...
  - id: mathematics
    label: Mathematics
```

The AI will pick the closest existing category, or propose a new one prefixed with `__NEW__:` — which requires explicit confirmation before being added.


## License
MIT

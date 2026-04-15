const API = "";
let allDocs = [];
let allCategories = [];
let pendingDocs = [];
let currentView = "home";
let activeCategory = null;
let activeTag = null;
let openCategories = new Set();
let searchTimeout = null;
let pendingRefreshInterval = null;

// Editor state
let editorSlug = null;
let editorOriginal = "";
let editorDirty = false;
let pendingNavAction = null;

// Modal state
let modalSlug = null;
let modalSuggestedId = null;

// ── Utilities ─────────────────────────────────────────────────────────────────

function setMobileContext(text) {
  const el = document.getElementById("mobile-context");
  if (el) el.textContent = text;
}

async function api(path, options) {
  const res = await fetch(API + path, options);
  if (!res.ok) throw new Error(res.statusText);
  return res.json();
}

function toggleSidebar() {
  document.getElementById("sidebar").classList.toggle("open");
  document.getElementById("overlay").classList.toggle("show");
}
function closeSidebar() {
  document.getElementById("sidebar").classList.remove("open");
  document.getElementById("overlay").classList.remove("show");
}

function escHtml(str) {
  return String(str)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}

function highlight(text, query) {
  if (!query) return escHtml(text);
  const escaped = escHtml(text);
  const re = new RegExp(
    escHtml(query).replace(/[.*+?^${}()|[\]\\]/g, "\\$&"),
    "gi",
  );
  return escaped.replace(re, (m) => `<mark>${m}</mark>`);
}

// Strip markdown syntax for plain text summary
function stripMarkdown(text) {
  return text
    .replace(/^>\s*/gm, "")
    .replace(/\*\*(.*?)\*\*/g, "$1")
    .replace(/\*(.*?)\*/g, "$1")
    .replace(/`(.*?)`/g, "$1")
    .replace(/\[(.*?)\]\(.*?\)/g, "$1")
    .trim();
}

function truncateSummary(text, max = 120) {
  const clean = stripMarkdown(text);
  if (clean.length <= max) return clean;
  return clean.slice(0, max).replace(/\s+\S*$/, "") + "…";
}

// Toast
let toastTimer = null;
function showToast(msg, type = "") {
  const el = document.getElementById("toast");
  el.textContent = msg;
  el.className = "toast show" + (type ? " " + type : "");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    el.className = "toast";
  }, 3000);
}

// Scroll to top button
document.getElementById("content").addEventListener("scroll", function () {
  const btn = document.getElementById("scroll-top-btn");
  btn.classList.toggle("visible", this.scrollTop > 300);
});

function scrollToTop() {
  document.getElementById("content").scrollTo({ top: 0, behavior: "smooth" });
}

// ── Editor guard — check before leaving ──────────────────────────────────────

function guardNavigation(action) {
  if (editorDirty) {
    pendingNavAction = action;
    document.getElementById("unsaved-modal").classList.add("show");
    return false;
  }
  return true;
}

function discardAndLeave() {
  editorDirty = false;
  document.getElementById("unsaved-modal").classList.remove("show");
  if (pendingNavAction) {
    pendingNavAction();
    pendingNavAction = null;
  }
}

async function saveAndLeave() {
  document.getElementById("unsaved-modal").classList.remove("show");
  await saveDoc();
  if (pendingNavAction) {
    pendingNavAction();
    pendingNavAction = null;
  }
}

// ── Draft ─────────────────────────────────────────────────────────────────────

async function submitDraft() {
  const topic = document.getElementById("draft-topic").value.trim();
  if (!topic) {
    showToast("Enter a topic first", "error");
    return;
  }

  const btn = document.getElementById("draft-btn");
  btn.disabled = true;
  btn.classList.add("loading");
  btn.querySelector(".draft-btn-label").textContent = "Generating…";

  try {
    const result = await api("/api/draft", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        topic,
        url: document.getElementById("draft-url").value.trim() || undefined,
        url_title:
          document.getElementById("draft-url-title").value.trim() || undefined,
        language: document.getElementById("draft-lang").value,
      }),
    });

    document.getElementById("draft-topic").value = "";
    document.getElementById("draft-url").value = "";
    document.getElementById("draft-url-title").value = "";

    showToast(`Draft ready: ${result.title}`, "success");
    await refreshPending();
    if (currentView === "home") showHome();
  } catch (e) {
    showToast("Generation failed: " + e.message, "error");
  } finally {
    btn.disabled = false;
    btn.classList.remove("loading");
    btn.querySelector(".draft-btn-label").textContent = "Generate draft";
  }
}

// ── Pending ───────────────────────────────────────────────────────────────────

async function refreshPending() {
  try {
    pendingDocs = await api("/api/pending");
  } catch (e) {
    pendingDocs = [];
  }
}

function renderPendingSection() {
  if (!pendingDocs || pendingDocs.length === 0) return "";
  const cards = pendingDocs
    .map((doc) => {
      const newCatBadge = doc.new_category
        ? `<span class="pending-new-cat">⚠ new category</span>`
        : "";
      const catLabel = doc.new_category
        ? doc.category.replace("__NEW__:", "")
        : doc.category || "uncategorized";
      return `
      <div class="pending-card" id="pending-card-${escHtml(doc.slug.replace("pending/", ""))}">
        <div class="pending-card-body" onclick="openDoc('${escHtml(doc.slug)}')">
          <div class="pending-card-title">${escHtml(doc.title)}</div>
          <div class="pending-card-meta">
            <span>${escHtml(catLabel)}</span>
            ${newCatBadge}
            ${(doc.tags || []).map((t) => `<span class="doc-tag">${escHtml(t)}</span>`).join("")}
          </div>
        </div>
        <div class="pending-actions" id="pending-actions-${escHtml(doc.slug.replace("pending/", ""))}">
          <button class="btn-approve" onclick="approveDoc('${escHtml(doc.slug.replace("pending/", ""))}', ${doc.new_category})">Approve</button>
          <button class="btn-reject" onclick="rejectDoc('${escHtml(doc.slug.replace("pending/", ""))}')">Reject</button>
        </div>
      </div>`;
    })
    .join("");
  return `
    <div class="section-block">
      <div class="pending-header">
        <span class="pending-title">Pending review</span>
        <span class="pending-count">${pendingDocs.length}</span>
      </div>
      ${cards}
    </div>`;
}

async function approveDoc(slug, hasNewCategory) {
  if (hasNewCategory) {
    const doc = pendingDocs.find((d) => d.slug === "pending/" + slug);
    openModal(slug, doc ? doc.category.replace("__NEW__:", "") : slug);
    return;
  }
  try {
    await api(`/api/review/${slug}/approve`, { method: "POST" });
    showToast("Document approved", "success");
    await refreshAll();
    if (currentView === "home") showHome();
    else if (currentView === "doc") resetHome();
  } catch (e) {
    showToast("Approval failed: " + e.message, "error");
  }
}

async function rejectDoc(slug) {
  try {
    await api(`/api/review/${slug}/reject`, { method: "POST" });
    showToast("Document rejected");
    await refreshPending();
    if (currentView === "home") showHome();
    else if (currentView === "doc") resetHome();
  } catch (e) {
    showToast("Rejection failed: " + e.message, "error");
  }
}

// ── New category modal ────────────────────────────────────────────────────────

function openModal(slug, suggestedId) {
  modalSlug = slug;
  modalSuggestedId = suggestedId;
  document.getElementById("modal-body").innerHTML =
    `The AI proposed a new category: <strong>${escHtml(suggestedId)}</strong>.<br>Confirm to add it to your vault, or cancel to keep the document pending.`;
  const label = suggestedId
    .split("-")
    .map((w) => (w ? w[0].toUpperCase() + w.slice(1) : ""))
    .join(" ");
  document.getElementById("modal-cat-label").value = label;
  document.getElementById("modal-overlay").classList.add("show");
}

function closeModal() {
  document.getElementById("modal-overlay").classList.remove("show");
  modalSlug = null;
  modalSuggestedId = null;
}

async function confirmNewCategory() {
  if (!modalSlug) return;
  const label = document.getElementById("modal-cat-label").value.trim();
  try {
    await api(`/api/review/${modalSlug}/approve`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        confirm_new_category: true,
        category_label: label,
      }),
    });
    closeModal();
    showToast("Document approved with new category", "success");
    await refreshAll();
    if (currentView === "home") showHome();
    else if (currentView === "doc") resetHome();
  } catch (e) {
    showToast("Failed: " + e.message, "error");
  }
}

// ── Nav ───────────────────────────────────────────────────────────────────────

function renderNav() {
  const nav = document.getElementById("cat-nav");
  nav.innerHTML = "";
  const allItem = document.createElement("div");
  allItem.className = "nav-item" + (currentView === "home" ? " active" : "");
  allItem.innerHTML = `<span class="nav-item-name">All documents</span><span class="nav-badge">${allDocs.length}</span>`;
  allItem.onclick = () => {
    if (
      !guardNavigation(() => {
        resetHome();
        closeSidebar();
      })
    )
      return;
    resetHome();
    closeSidebar();
  };
  nav.appendChild(allItem);

  const label = document.createElement("div");
  label.className = "nav-label";
  label.textContent = "Topics";
  nav.appendChild(label);

  allCategories.forEach((cat) => {
    const catRow = document.createElement("div");
    const isOpen = openCategories.has(cat.id);
    const isCatActive = activeCategory === cat.id && currentView === "category";
    catRow.className =
      "nav-category" + (isOpen ? " open" : "") + (isCatActive ? " active" : "");
    catRow.innerHTML = `
      <div class="nav-category-left">
        <span class="nav-category-arrow">▶</span>
        <span class="nav-category-name">${cat.label}</span>
      </div>
      <span class="nav-badge">${cat.count}</span>`;
    catRow.onclick = () => {
      if (!guardNavigation(() => toggleCategory(cat))) return;
      toggleCategory(cat);
    };
    nav.appendChild(catRow);

    const tagsDiv = document.createElement("div");
    tagsDiv.className = "nav-tags" + (isOpen ? " open" : "");
    tagsDiv.id = `tags-${cat.id}`;
    (cat.tags || []).forEach(({ tag, count }) => {
      const tagRow = document.createElement("div");
      const isTagActive = activeTag === tag && currentView === "tag";
      tagRow.className = "nav-tag" + (isTagActive ? " active" : "");
      tagRow.innerHTML = `<span class="nav-tag-name">${tag}</span><span class="nav-tag-count">${count}</span>`;
      tagRow.onclick = (e) => {
        e.stopPropagation();
        if (
          !guardNavigation(() => {
            activeTag = tag;
            activeCategory = null;
            showTagView(tag);
            closeSidebar();
          })
        )
          return;
        activeTag = tag;
        activeCategory = null;
        showTagView(tag);
        closeSidebar();
      };
      tagsDiv.appendChild(tagRow);
    });
    nav.appendChild(tagsDiv);
  });
}

function toggleCategory(cat) {
  if (openCategories.has(cat.id)) {
    openCategories.delete(cat.id);
    if (activeCategory === cat.id) {
      activeCategory = null;
      currentView = "home";
      showHome();
      return;
    }
  } else {
    openCategories.add(cat.id);
  }
  activeCategory = cat.id;
  activeTag = null;
  showCategoryView(cat);
}

function resetHome() {
  editorDirty = false;
  editorSlug = null;
  clearSearch();
  activeCategory = null;
  activeTag = null;
  showHome();
  closeSidebar();
}

function clearSearch() {
  document.getElementById("search-input").value = "";
}

// ── Views ─────────────────────────────────────────────────────────────────────

function showHome() {
  currentView = "home";
  setMobileContext("");
  renderNav();

  // Recently added: top 6 by modified_at descending
  const recent = [...allDocs]
    .filter((d) => d.modified_at)
    .sort((a, b) => b.modified_at - a.modified_at)
    .slice(0, 6);

  const recentSection = recent.length
    ? `
    <div class="section-block">
      <div class="section-title">Recently added</div>
      <div class="doc-grid">
        ${recent.map((doc) => docCard(doc)).join("")}
      </div>
    </div>`
    : "";

  document.getElementById("content").innerHTML = `
    <div class="home">
      ${renderPendingSection()}
      ${recentSection}
      <div class="section-block">
        <div class="section-title">All documents</div>
        <div class="doc-grid">
          ${allDocs.map((doc) => docCard(doc)).join("")}
        </div>
      </div>
    </div>`;
}

function docCard(doc) {
  const summary = doc.summary
    ? `<div class="doc-card-summary">${escHtml(truncateSummary(doc.summary))}</div>`
    : "";
  return `
    <div class="doc-card" onclick="openDoc('${escHtml(doc.slug)}')">
      <div class="doc-card-title">${escHtml(doc.title)}</div>
      ${summary}
      <div class="doc-card-tags">
        ${(doc.tags || []).map((t) => `<span class="doc-tag">${escHtml(t)}</span>`).join("")}
      </div>
    </div>`;
}

async function showCategoryView(cat) {
  clearSearch();
  currentView = "category";
  setMobileContext(cat.label);
  renderNav();
  const content = document.getElementById("content");
  content.innerHTML = `<div class="loading"><span class="loading-dot"></span><span class="loading-dot"></span><span class="loading-dot"></span></div>`;
  try {
    const docs = await api(`/api/category/${encodeURIComponent(cat.id)}`);
    content.innerHTML = `<div class="home"><div class="doc-grid">${docs.map((doc) => docCard(doc)).join("")}</div></div>`;
  } catch (e) {
    content.innerHTML = `<div class="no-results">Failed to load category.</div>`;
  }
}

async function showTagView(tag) {
  clearSearch();
  currentView = "tag";
  setMobileContext(tag);
  renderNav();
  const content = document.getElementById("content");
  content.innerHTML = `<div class="loading"><span class="loading-dot"></span><span class="loading-dot"></span><span class="loading-dot"></span></div>`;
  try {
    const docs = await api(`/api/tag/${encodeURIComponent(tag)}`);
    content.innerHTML = `<div class="home"><div class="doc-grid">${docs.map((doc) => docCard(doc)).join("")}</div></div>`;
  } catch (e) {
    content.innerHTML = `<div class="no-results">Failed to load tag.</div>`;
  }
}

function handleSearch(q) {
  clearTimeout(searchTimeout);
  if (!q.trim()) {
    showHome();
    return;
  }
  searchTimeout = setTimeout(() => runSearch(q.trim()), 200);
}

async function runSearch(q) {
  if (!guardNavigation(() => runSearch(q))) return;
  currentView = "search";
  activeCategory = null;
  activeTag = null;
  setMobileContext(`"${q}"`);
  renderNav();
  const content = document.getElementById("content");
  content.innerHTML = `<div class="loading"><span class="loading-dot"></span><span class="loading-dot"></span><span class="loading-dot"></span></div>`;
  try {
    const results = await api(`/api/search?q=${encodeURIComponent(q)}`);
    if (!results.length) {
      content.innerHTML = `<div class="search-results"><div class="no-results">No results for "<strong>${escHtml(q)}</strong>"</div></div>`;
      return;
    }
    content.innerHTML = `
      <div class="search-results">
        <div class="section-title" style="margin-bottom:16px">${results.length} result${results.length !== 1 ? "s" : ""} for "${escHtml(q)}"</div>
        ${results
          .map((doc) => {
            const titleHtml = highlight(
              doc.title,
              doc.match_in === "title" ? q : "",
            );
            const snippetHtml = doc.snippet
              ? `<div class="result-snippet">${highlight(doc.snippet, q)}</div>`
              : "";
            return `
            <div class="result-item" onclick="openDoc('${escHtml(doc.slug)}')">
              <div class="result-title">${titleHtml}</div>
              ${snippetHtml}
              <div class="result-tags">${(doc.tags || []).map((t) => `<span class="doc-tag">${escHtml(t)}</span>`).join("")}</div>
            </div>`;
          })
          .join("")}
      </div>`;
  } catch (e) {
    content.innerHTML = `<div class="no-results">Search failed.</div>`;
  }
}

// ── Doc viewer ────────────────────────────────────────────────────────────────

async function openDoc(slug) {
  if (!guardNavigation(() => openDoc(slug))) return;
  clearSearch();
  currentView = "doc";
  closeSidebar();
  editorDirty = false;
  editorSlug = null;
  const content = document.getElementById("content");
  content.innerHTML = `<div class="loading"><span class="loading-dot"></span><span class="loading-dot"></span><span class="loading-dot"></span></div>`;
  content.scrollTop = 0;

  const isPending = slug.startsWith("pending/");
  const bareSlug = isPending ? slug.replace("pending/", "") : slug;

  try {
    const doc = await api(`/api/doc/${slug}`);
    setMobileContext(doc.title);
    renderDocView(doc, slug, isPending, bareSlug);
  } catch (e) {
    content.innerHTML = `<div class="no-results">Document not found.</div>`;
  }
}

function renderDocView(doc, slug, isPending, bareSlug) {
  const content = document.getElementById("content");

  const pendingBanner = isPending
    ? `
    <div class="pending-banner">
      <span class="pending-banner-text">⏳ Pending review</span>
      <div class="pending-banner-actions">
        <button class="btn-approve" onclick="approveDoc('${escHtml(bareSlug)}', ${!!pendingDocs.find((d) => d.slug === "pending/" + bareSlug)?.new_category})">Approve</button>
        <button class="btn-reject" onclick="rejectDoc('${escHtml(bareSlug)}')">Reject</button>
      </div>
    </div>`
    : "";

  const relatedHtml =
    doc.related && doc.related.length
      ? `
    <div class="related-section">
      <div class="related-title">Connected concepts</div>
      <div class="related-chips">
        ${doc.related.map((r) => `<div class="related-chip" onclick="openDoc('${escHtml(r.slug)}')">${escHtml(r.title)}</div>`).join("")}
      </div>
    </div>`
      : "";

  const editBtn = isPending
    ? ""
    : `<button class="btn-edit" onclick="openEditor('${escHtml(slug)}')">Edit</button>`;

  content.innerHTML = `
    <div class="doc-view">
      <div class="doc-nav-bar">
        <button class="btn-back" onclick="resetHome()">← Back</button>
        ${editBtn}
      </div>
      ${pendingBanner}
      <div class="doc-header">
        <div class="doc-title">${escHtml(doc.title)}</div>
        <div class="doc-meta-row">
          <div class="doc-header-tags">
            ${(doc.tags || []).map((t) => `<span class="doc-header-tag" onclick="activeTag='${escHtml(t)}';showTagView('${escHtml(t)}')">${escHtml(t)}</span>`).join("")}
          </div>
        </div>
      </div>
      ${relatedHtml}
      <div class="doc-body">${doc.html}</div>
    </div>`;

  content.querySelectorAll(".wiki-link").forEach((a) => {
    a.addEventListener("click", (e) => {
      e.preventDefault();
      openDoc(a.dataset.slug);
    });
  });
}

// ── Editor ────────────────────────────────────────────────────────────────────

async function openEditor(slug) {
  const content = document.getElementById("content");
  content.innerHTML = `<div class="loading"><span class="loading-dot"></span><span class="loading-dot"></span><span class="loading-dot"></span></div>`;
  try {
    const doc = await api(`/api/doc/raw/${slug}`);
    editorSlug = slug;
    editorOriginal = doc.raw;
    editorDirty = false;
    currentView = "edit";
    setMobileContext("Editing: " + doc.title);
    renderEditor(doc.raw, slug);
  } catch (e) {
    showToast("Failed to load document for editing", "error");
    openDoc(slug);
  }
}

function renderEditor(raw, slug) {
  const content = document.getElementById("content");
  content.scrollTop = 0;
  content.innerHTML = `
    <div class="doc-editor">
      <div class="editor-nav-bar">
        <button class="btn-back btn-cancel-edit" onclick="cancelEdit('${escHtml(slug)}')">← Cancel</button>
        <span class="editor-unsaved" id="editor-unsaved" style="display:none">● unsaved</span>
        <button class="btn-save" id="save-btn" onclick="saveDoc()">Save</button>
      </div>
      <div class="editor-label">Markdown source</div>
      <textarea class="editor-textarea" id="editor-textarea" spellcheck="false">${escHtml(raw)}</textarea>
    </div>`;

  const ta = document.getElementById("editor-textarea");
  ta.addEventListener("input", () => {
    editorDirty = ta.value !== editorOriginal;
    document.getElementById("editor-unsaved").style.display = editorDirty
      ? ""
      : "none";
  });
}

async function saveDoc() {
  const ta = document.getElementById("editor-textarea");
  if (!ta || !editorSlug) return;
  const btn = document.getElementById("save-btn");
  if (btn) btn.disabled = true;
  try {
    await api(`/api/doc/${editorSlug}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ content: ta.value }),
    });
    editorOriginal = ta.value;
    editorDirty = false;
    document.getElementById("editor-unsaved").style.display = "none";
    showToast("Saved", "success");
    // Return to view mode
    openDoc(editorSlug);
  } catch (e) {
    showToast("Save failed: " + e.message, "error");
    if (btn) btn.disabled = false;
  }
}

function cancelEdit(slug) {
  if (editorDirty) {
    pendingNavAction = () => openDoc(slug);
    document.getElementById("unsaved-modal").classList.add("show");
    return;
  }
  openDoc(slug);
}

// ── Data refresh ──────────────────────────────────────────────────────────────

async function refreshAll() {
  [allDocs, allCategories, pendingDocs] = await Promise.all([
    api("/api/docs"),
    api("/api/categories"),
    api("/api/pending"),
  ]);
  renderNav();
}

// ── Events ────────────────────────────────────────────────────────────────────

document
  .getElementById("search-input")
  .addEventListener("input", (e) => handleSearch(e.target.value));
document.getElementById("draft-topic").addEventListener("keydown", (e) => {
  if (e.key === "Enter") submitDraft();
});

async function init() {
  try {
    await refreshAll();
    showHome();
    pendingRefreshInterval = setInterval(async () => {
      await refreshPending();
      if (currentView === "home") showHome();
    }, 30000);
  } catch (e) {
    document.getElementById("content").innerHTML = `
      <div class="no-results">
        <strong>Cannot connect to server.</strong><br><br>
        Make sure the Brain server is running.
      </div>`;
  }
}

init();

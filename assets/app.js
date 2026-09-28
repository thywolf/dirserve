// dirserve UI. Vanilla, no build step, no framework.
//
// Two rules are load-bearing:
//  1. Every name from the filesystem reaches the DOM via textContent. A filename
//     is attacker-controlled, so there is no innerHTML on any of them; the only
//     innerHTML calls below write icon markup this file itself defines.
//  2. Data comes from directory URLs with Accept: application/json — the same
//     URLs curl uses. This program has no API path, so the UI has no secret one.
//
// State lives in the URL hash (#/path/to/file), so any view can be linked.

const el = (id) => document.getElementById(id);
const ui = {
  root: el("root-path"), tree: el("tree"), filter: el("filter"),
  head: el("head"), crumb: el("crumb"), meta: el("meta"),
  actions: el("actions"), pane: el("pane"),
  drawer: el("btn-drawer"), back: el("btn-back"), scrim: el("scrim"),
  theme: el("btn-theme"), filterClear: el("filter-clear"),
  sidebar: document.querySelector(".sidebar"),
};

// The server renders --max-preview-bytes into the shell; anything larger is
// offered as a download rather than pulled into the browser. Zero is a real
// answer ("no inline previews"), so only a missing or malformed attribute
// falls back to the default.
const MAX_PREVIEW = (() => {
  const n = Number(document.body.dataset.maxPreview);
  return Number.isFinite(n) && n >= 0 ? n : 1048576;
})();

// The phone breakpoint, declared with the other module constants rather than
// beside the drawer code: show() reads it during the initial load, which runs
// before the bottom of this file is evaluated.
const narrow = matchMedia("(max-width: 760px)");

// ---------------------------------------------------------------- icons ----
// Inline SVG only: no image files, no icon font, nothing extra to load.

const svg = (d) =>
  '<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" ' +
  'stroke-linecap="round" stroke-linejoin="round">' + d + '</svg>';

const ICON = {
  dir: svg('<path d="M2 4.6A1.3 1.3 0 0 1 3.3 3.3h2.6l1.3 1.5h5.5A1.3 1.3 0 0 1 14 6.1v5.6a1.3 1.3 0 0 1-1.3 1.3H3.3A1.3 1.3 0 0 1 2 11.7z"/>'),
  file: svg('<path d="M4 2.6h4.4L12 6.2v7.2a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V3.6a1 1 0 0 1 1-1z"/><path d="M8.3 2.7v3.4H12"/>'),
  chevron: svg('<path d="m6.2 3.6 4 4.4-4 4.4"/>'),
  copy: svg('<rect x="5.4" y="5.4" width="8" height="8" rx="1.6"/><path d="M10.6 5.4V4A1.6 1.6 0 0 0 9 2.4H4A1.6 1.6 0 0 0 2.4 4v5A1.6 1.6 0 0 0 4 10.6h1.4"/>'),
  download: svg('<path d="M8 2.4v7.2m0 0 2.6-2.6M8 9.6 5.4 7"/><path d="M2.6 11v1.4a1.2 1.2 0 0 0 1.2 1.2h8.4a1.2 1.2 0 0 0 1.2-1.2V11"/>'),
  link: svg('<path d="M6.6 9.4a2.6 2.6 0 0 0 3.9.3l1.8-1.8a2.6 2.6 0 1 0-3.7-3.7l-1 1"/><path d="M9.4 6.6a2.6 2.6 0 0 0-3.9-.3L3.7 8.1a2.6 2.6 0 1 0 3.7 3.7l1-1"/>'),
  // One glyph per file family, so a listing scans by shape before size.
  image: svg('<rect x="2.5" y="3.5" width="11" height="9" rx="1.5"/><circle cx="6" cy="6.6" r="1.1"/><path d="m4.5 10.8 2.3-2.3 1.9 1.9 1.9-1.9 1.9 1.9"/>'),
  video: svg('<rect x="2.5" y="3.5" width="11" height="9" rx="1.5"/><path d="M7 6.1v3.8L10.2 8z"/>'),
  audio: svg('<path d="M6.6 11.4V4.2l4.8-1v7.2"/><circle cx="5" cy="11.4" r="1.6"/><circle cx="9.8" cy="10.4" r="1.6"/>'),
  pdf: svg('<path d="M4 2.6h4.4L12 6.2v7.2a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V3.6a1 1 0 0 1 1-1z"/><path d="M8.3 2.7v3.4H12"/><path d="M5.7 10.7h2.6c1.6 0 1.6-2.4 0-2.4H5.7"/>'),
  archive: svg('<rect x="2.5" y="2.8" width="11" height="3" rx="0.8"/><path d="M3.6 5.8v6.4a1 1 0 0 0 1 1h6.8a1 1 0 0 0 1-1V5.8"/><path d="M6.5 8.4h3"/>'),
  code: svg('<path d="m5.6 5.2-2.9 2.8 2.9 2.8M10.4 5.2l2.9 2.8-2.9 2.8"/>'),
  data: svg('<path d="M6.2 2.9c-1.6 0-2.1.8-2.1 2v1.5c0 .8-.4 1.3-1.3 1.6.9.3 1.3.8 1.3 1.6v1.5c0 1.2.5 2 2.1 2"/><path d="M9.8 2.9c1.6 0 2.1.8 2.1 2v1.5c0 .8.4 1.3 1.3 1.6-.9.3-1.3.8-1.3 1.6v1.5c0 1.2-.5 2-2.1 2"/>'),
  doc: svg('<path d="M4 2.6h4.4L12 6.2v7.2a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V3.6a1 1 0 0 1 1-1z"/><path d="M8.3 2.7v3.4H12"/><path d="M5.7 8.2h4.6M5.7 10.4h3.2"/>'),
};

// Which glyph a file gets. The server's content-type policy stays the authority
// for what a file *is*; this only decides how the name is drawn.
const FAM_BY_EXT = {
  png: "image", jpg: "image", jpeg: "image", gif: "image", webp: "image",
  avif: "image", bmp: "image", ico: "image", svg: "image",
  mp4: "video", webm: "video", mov: "video", mkv: "video", avi: "video",
  mp3: "audio", wav: "audio", flac: "audio", ogg: "audio", m4a: "audio", opus: "audio",
  pdf: "pdf",
  zip: "archive", gz: "archive", tar: "archive", xz: "archive", zst: "archive",
  "7z": "archive", bz2: "archive", rar: "archive",
  go: "code", rs: "code", c: "code", h: "code", cpp: "code", java: "code",
  rb: "code", py: "code", js: "code", mjs: "code", ts: "code", tsx: "code",
  jsx: "code", sh: "code", bash: "code", zsh: "code", fish: "code", ps1: "code",
  sql: "code", lua: "code", php: "code", pl: "code", swift: "code", kt: "code",
  json: "data", yaml: "data", yml: "data", toml: "data", xml: "data",
  csv: "data", tsv: "data", lock: "data", conf: "data", ini: "data", env: "data",
  md: "doc", txt: "doc", log: "doc", rst: "doc", text: "doc",
};

// iconFor picks the glyph for a name: directories are folders, files get their
// family's icon, and anything unknown falls back to the plain file shape.
function iconFor(name, isDir) {
  if (isDir) return { html: ICON.dir, fam: "" };
  const dot = name.lastIndexOf(".");
  const fam = dot > 0 ? FAM_BY_EXT[name.slice(dot + 1).toLowerCase()] || "" : "";
  return { html: fam ? ICON[fam] : ICON.file, fam };
}

// ------------------------------------------------------------- helpers ----

const enc = (p) => p.split("/").filter(Boolean).map(encodeURIComponent).join("/");
const urlOf = (p) => "/" + enc(p);
const absUrl = (p) => location.origin + urlOf(p);
const nameOf = (p) => p.split("/").pop();
// The parent of a root-relative path; the root's parent is the root itself.
const parentOf = (p) => {
  const i = p.lastIndexOf("/");
  return i < 0 ? "" : p.slice(0, i);
};

// With token auth on, every URL the UI links to needs the token or Raw/Download
// would 401 against a fresh request.
const auth = () => {
  const t = new URLSearchParams(location.search).get("access_token");
  return t ? "access_token=" + encodeURIComponent(t) : "";
};

function humanSize(n) {
  if (n < 1024) return n + " B";
  const u = ["KB", "MB", "GB", "TB"];
  let v = n / 1024, i = 0;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return (v >= 10 ? v.toFixed(0) : v.toFixed(1)) + " " + u[i];
}

function humanTime(unix) {
  if (!unix) return "";
  const d = new Date(unix * 1000);
  const days = (Date.now() - d) / 86400000;
  if (days < 1) return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  if (days < 7) return d.toLocaleDateString([], { weekday: "short", hour: "2-digit", minute: "2-digit" });
  return d.toLocaleDateString([], { year: "numeric", month: "short", day: "numeric" });
}


// A short human label for a file's kind, derived from its extension. This
// mirrors the server's content-type policy closely enough to be honest without
// duplicating it: an unknown extension simply has no label.
const KIND_BY_EXT = {
  png: "PNG image", jpg: "JPEG image", jpeg: "JPEG image", gif: "GIF image",
  webp: "WebP image", avif: "AVIF image", bmp: "bitmap", ico: "icon", svg: "SVG image",
  mp4: "MP4 video", webm: "WebM video", mov: "QuickTime video", mkv: "Matroska video",
  mp3: "MP3 audio", wav: "WAV audio", flac: "FLAC audio", ogg: "Ogg audio", m4a: "AAC audio",
  pdf: "PDF", zip: "zip", gz: "gzip", tar: "tar", xz: "xz", zst: "zstd", "7z": "7-Zip",
  wasm: "WebAssembly", sh: "shell", py: "Python", js: "JavaScript", ts: "TypeScript",
  go: "Go", rs: "Rust", c: "C", h: "C header", cpp: "C++", java: "Java", rb: "Ruby",
  json: "JSON", yaml: "YAML", yml: "YAML", toml: "TOML", md: "Markdown", txt: "text",
  conf: "config", ini: "config", env: "dotenv", sql: "SQL", lock: "lockfile",
};

function fileKind(name) {
  const dot = name.lastIndexOf(".");
  if (dot < 1) return "";
  return KIND_BY_EXT[name.slice(dot + 1).toLowerCase()] || "";
}
const listing = async (path) => {
  const res = await fetch(urlOf(path), { headers: { Accept: "application/json" } });
  if (!res.ok) throw new Error("status " + res.status);
  return res.json();
};

// ------------------------------------------------------------- the tree ----
//
// The tree is a flat list of rows. A directory's children are inserted after it
// and collapsing hides them, because a .row is a flex container and a nested
// .row inside one would become a flex item rather than a list line.
//
// Any structural change re-renders the whole visible tree from the model. At
// these sizes (a few thousand rows at most) that is simpler to reason about than
// incremental insertion, and it cannot drift out of sync with the model.

const nodes = new Map(); // path -> node
let rootLoaded = false;
let selected = "";      // path currently previewed/highlighted

function nodeFor(entry, parent) {
  return {
    path: parent ? parent + "/" + entry.name : entry.name,
    name: entry.name,
    isDir: entry.type === "dir",
    size: entry.size,
    mtime: entry.mtime_unix,
    depth: parent === "" ? 0 : parent.split("/").length,
    loaded: false,
    open: false,
    truncated: false,
    children: null,
    row: null,
  };
}

function makeRow(node) {
  const row = document.createElement("div");
  row.className = "row " + (node.isDir ? "dir" : "file") + (node.open ? " open" : "") +
    (node.path === selected ? " selected" : "");
  row.dataset.path = node.path;
  row.style.paddingLeft = 8 + node.depth * 14 + "px";
  row.setAttribute("role", "treeitem");

  // Indentation guides: one hairline per ancestor level.
  for (let d = 1; d <= node.depth; d++) {
    const rail = document.createElement("span");
    rail.className = "rail";
    rail.style.left = 8 + (d - 1) * 14 + 14 + "px";
    row.appendChild(rail);
  }

  const twist = document.createElement("span");
  twist.className = "twist" + (node.isDir ? "" : " hide");
  twist.innerHTML = ICON.chevron;
  const icon = iconFor(node.name, node.isDir);
  const ico = document.createElement("span");
  ico.className = "ico" + (icon.fam ? " fam-" + icon.fam : "");
  ico.innerHTML = icon.html;
  const label = document.createElement("span");
  label.className = "name";
  label.textContent = node.name;
  row.append(twist, ico, label);

  if (!node.isDir) {
    const size = document.createElement("span");
    size.className = "size";
    size.textContent = humanSize(node.size);
    row.appendChild(size);
  }

  // A directory expands or collapses; a file previews.
  row.addEventListener("click", () => (node.isDir ? toggle(node) : show(node.path)));
  node.row = row;
  return row;
}

function noteRow(text, depth) {
  const d = document.createElement("div");
  d.className = "row note";
  d.style.paddingLeft = 21 + depth * 14 + "px";
  d.textContent = text;
  return d;
}

// renderTree walks the open part of the model and rebuilds the row list. A
// node's filter state comes from filterMatch, computed on the model.
function renderTree() {
  const q = ui.filter.value.trim().toLowerCase();
  const frag = document.createDocumentFragment();

  const walk = (list) => {
    for (const node of list) {
      if (q && !filterMatch(node, q)) continue; // subtree entirely filtered out
      frag.appendChild(makeRow(node));
      if (node.isDir && node.open && node.children) {
        walk(node.children);
        if (node.truncated) frag.appendChild(noteRow("… truncated at 5,000 entries", node.depth + 1));
      }
    }
  };

  walk([...nodes.values()].filter((n) => n.depth === 0));
  if (!frag.childNodes.length) {
    frag.appendChild(noteRow(q ? "no match" : "cannot read directory", 0));
  }
  ui.tree.replaceChildren(frag);
  const sel = ui.tree.querySelector(".row.selected");
  if (sel) sel.scrollIntoView({ block: "nearest" });
}

// filterMatch is true when a node matches the query itself or anything under it.
function filterMatch(node, q) {
  if (node.name.toLowerCase().includes(q)) return true;
  if (!node.isDir || !node.loaded || !node.children) return false;
  return node.children.some((c) => filterMatch(c, q));
}

async function loadChildren(node) {
  try {
    const data = await listing(node.path);
    node.children = data.entries.map((e) => nodeFor(e, node.path));
    for (const c of node.children) nodes.set(c.path, c);
    node.truncated = !!data.truncated;
  } catch {
    node.children = [];
  }
  node.loaded = true;
}

async function toggle(node, force) {
  const want = force === undefined ? !node.open : force;
  if (want && !node.loaded) await loadChildren(node);
  node.open = want;
  renderTree();
}

async function loadRoot() {
  try {
    const data = await listing("");
    nodes.clear();
    for (const e of data.entries) {
      const n = nodeFor(e, "");
      nodes.set(n.path, n);
    }
    rootTruncated = !!data.truncated;
    rootLoaded = true;
  } catch {
    rootLoaded = true;
  }
}

let rootTruncated = false;

// Expand every ancestor so a deep link arrives with its path already open.
async function reveal(path) {
  const segs = path.split("/").filter(Boolean);
  let acc = "";
  for (const seg of segs) {
    acc = acc ? acc + "/" + seg : seg;
    const n = nodes.get(acc);
    if (!n) break;
    if (n.isDir && !n.open) {
      await loadChildren(n);
      n.open = true;
    }
  }
}

// -------------------------------------------------------------- preview ----

// A hash segment that is not valid percent-encoding ("%zz") would make
// decodeURIComponent throw and, before any error handler could run, take the
// whole UI down with it: hashPath runs at module level and inside the URL
// change handlers. Decode what decodes and leave the rest literal — such a
// segment simply matches no file, which is what a malformed link deserves.
const decodeSeg = (seg) => {
  try {
    return decodeURIComponent(seg);
  } catch {
    return seg;
  }
};

const hashPath = () =>
  location.hash.replace(/^#/, "").replace(/^\//, "")
    .split("/").filter(Boolean).map(decodeSeg).join("/");

function setHash(path, replace) {
  const next = "#" + urlOf(path);
  if (location.hash === next) return;
  history[replace ? "replaceState" : "pushState"](null, "", next);
}

async function show(path, replace) {
  if (!rootLoaded) await loadRoot();
  await reveal(path);
  setHash(path, replace);
  selected = path;
  renderTree();
  // Once the user has actually opened something, the root shows its listing
  // rather than the welcome state again.
  // The head ships hidden and only appears with a real selection: on the
  // untouched root the welcome state is the whole show, and a one-glyph
  // breadcrumb above it is noise, not navigation.
  if (path !== "") {
    ui.root.dataset.touched = "1";
    ui.head.hidden = false;
  }
  // The up chevron only means something once there is a selection; on the
  // untouched root it would advertise a level above the top.
  ui.back.hidden = path === "";
  ui.pane.scrollTop = 0;
  // A tap on a row inside the drawer navigates, and the preview is what the
  // user came for, so the drawer closes and the pane reclaims the screen.
  if (narrow.matches) setDrawer(false);
  renderHead(path);

  const node = nodes.get(path);
  const isDir = path === "" || !!(node && node.isDir);

  // Every render wraps its content in one .view element: it gives the pane's
  // children a definite height to fill (so a short text file's background
  // covers the pane instead of stopping after the last line) and a single
  // place for the navigation transition.
  const view = document.createElement("div");
  view.className = "view";

  // The pane is cleared on every navigation, so the welcome state is built here
  // rather than shipped as static markup that the first render throws away.
  if (path === "" && !ui.root.dataset.touched) {
    view.appendChild(welcome());
    ui.pane.replaceChildren(view);
    return;
  }

  ui.pane.replaceChildren(view);
  if (isDir) renderDir(view, path, node);
  else await renderFile(view, path, node);
}

// The welcome state: what this is, and how to drive it. Built in JS because the
// pane is re-rendered on every navigation, so static markup would not survive
// the first selection.
function welcome() {
  const d = document.createElement("div");
  d.className = "blank";

  const glyph = document.createElement("div");
  glyph.className = "glyph";
  glyph.innerHTML = ICON.dir;
  glyph.setAttribute("aria-hidden", "true");

  const title = document.createElement("h2");
  title.textContent = "Read-only, by design";

  const lead = document.createElement("p");
  lead.textContent = "Pick a file to preview it here, or take the whole tree from a shell:";

  // The command is the product — clicking it copies it, so the shell one-liner
  // never has to be re-typed.
  const cmd = document.createElement("button");
  cmd.type = "button";
  cmd.className = "curlchip";
  cmd.title = "Copy";
  cmd.setAttribute("aria-label", "Copy the curl command for this directory");
  const code = document.createElement("code");
  code.textContent = "curl -fsSL " + absUrl("") + "…";
  cmd.appendChild(code);
  cmd.addEventListener("click", async () => {
    const full = "curl -fsSL " + absUrl("") + (auth() ? "?" + auth() : "");
    try {
      await navigator.clipboard.writeText(full);
      cmd.classList.add("done");
      setTimeout(() => cmd.classList.remove("done"), 1200);
    } catch {
      window.prompt("Copy command", full);
    }
  });

  d.append(glyph, title, lead, cmd);
  // A phone has no keyboard; the legend would advertise keys it cannot use.
  if (!narrow.matches) d.append(keyLegend());
  return d;
}

function keyLegend() {
  const keys = document.createElement("p");
  keys.className = "keys";
  for (const [combo, label] of [["/", "filter"], ["↑ ↓", "move"], ["↵", "open"], ["esc", "clear"]]) {
    const span = document.createElement("span");
    for (const k of combo.split(" ")) {
      const kbd = document.createElement("kbd");
      kbd.textContent = k;
      span.appendChild(kbd);
    }
    span.appendChild(document.createTextNode(" " + label));
    keys.appendChild(span);
  }
  return keys;
}

function blank(host, icon, title, text) {
  const d = document.createElement("div");
  d.className = "blank";
  const g = document.createElement("div");
  g.className = "glyph";
  g.innerHTML = ICON[icon];
  const h = document.createElement("h2");
  h.textContent = title;
  const p = document.createElement("p");
  p.textContent = text;
  d.append(g, h, p);
  host.appendChild(d);
}

function curlHint(path) {
  const p = document.createElement("p");
  p.className = "hint";
  const code = document.createElement("code");
  code.textContent = "curl -fsSL " + absUrl(path) + (auth() ? "?" + auth() : "");
  p.appendChild(code);
  return p;
}

// A directory preview lists this directory's own entries, so the pane is useful
// even with the tree collapsed.
function renderDir(host, path, node) {
  // The root has no node of its own in the map — its entries are the depth-0
  // nodes — so read the children from whichever source applies.
  const kids = node && node.loaded ? node.children
    : path === "" ? [...nodes.values()].filter((n) => n.depth === 0)
    : [];
  if (!kids || !kids.length) {
    blank(host, "dir", "Empty directory", "Nothing here. From a shell:");
    host.appendChild(curlHint(path));
    return;
  }
  const list = document.createElement("div");
  list.className = "dirlist";

  // A heading gives the pane a title when it is showing a directory, so the
  // eye has somewhere to land before the rows start.
  const label = document.createElement("div");
  label.className = "dirlabel";
  label.textContent = kids.length + (kids.length === 1 ? " item" : " items");
  list.appendChild(label);

  for (const child of kids) {
    const row = document.createElement("div");
    row.className = "row " + (child.isDir ? "dir" : "file");
    const icon = iconFor(child.name, child.isDir);
    const ico = document.createElement("span");
    ico.className = "ico" + (icon.fam ? " fam-" + icon.fam : "");
    ico.innerHTML = icon.html;
    const name = document.createElement("span");
    name.className = "name";
    name.textContent = child.name;
    row.append(ico, name);

    if (!child.isDir) {
      const size = document.createElement("span");
      size.className = "size";
      size.textContent = humanSize(child.size);
      row.appendChild(size);
    } else {
      const chev = document.createElement("span");
      chev.className = "go";
      chev.innerHTML = ICON.chevron;
      row.appendChild(chev);
    }

    row.addEventListener("click", async () => {
      if (child.isDir) await toggle(child, true);
      show(child.path);
    });
    list.appendChild(row);
  }

  if (node && node.truncated) {
    const note = document.createElement("div");
    note.className = "row note";
    note.style.padding = "8px 16px 0";
    note.textContent = "listing truncated at 5,000 entries";
    list.appendChild(note);
  }
  host.appendChild(list);
}


async function renderFile(host, path, node) {
  const url = absUrl(path) + (auth() ? "?" + auth() : "");
  const head = await fetch(url, { method: "HEAD" }).catch(() => null);
  const type = (head && head.headers.get("Content-Type")) || "";
  const size = Number((head && head.headers.get("Content-Length")) || (node && node.size) || 0);
  const base = type.split(";")[0];

  if (size > MAX_PREVIEW) {
    return blank(host, "file", "Too large to preview",
      humanSize(size) + " is over the " + humanSize(MAX_PREVIEW) +
      " preview limit — download it, or fetch it with curl.");
  }
  if (base === "text/plain") return renderText(host, url);
  if (base.startsWith("image/")) return renderMedia(host, "img", url, nameOf(path));
  if (base.startsWith("video/")) return renderMedia(host, "video", url, nameOf(path));
  if (base.startsWith("audio/")) return renderMedia(host, "audio", url, nameOf(path));
  if (base === "application/pdf") return renderPdf(host, url);
  return blank(host, "file", "Binary file",
    humanSize(size) + " of " + (base || "unknown type") + " — download it, or fetch it with curl.");
}

async function renderText(host, url) {
  const res = await fetch(url);
  if (!res.ok) return blank(host, "file", "Cannot read", "The file could not be fetched.");
  const text = await res.text();
  // The server already decided this is text, but a decode that produced
  // replacement characters means the bytes were not text after all.
  if (text.includes("�")) {
    return blank(host, "file", "Binary file", "This file is not text — download it instead.");
  }
  const pre = document.createElement("pre");
  pre.className = "code";
  pre.setAttribute("aria-label", "file contents");
  const code = document.createElement("code");
  for (const line of text.split("\n")) {
    const div = document.createElement("span");
    div.className = "l";
    div.textContent = line;
    code.appendChild(div);
  }
  pre.appendChild(code);
  host.appendChild(pre);
}

function renderMedia(host, tag, url, alt) {
  const wrap = document.createElement("div");
  wrap.className = "media";
  const m = document.createElement(tag);
  if (tag === "img") {
    m.alt = alt;
    // A directory of images previews one at a time; a lazy decode keeps the
    // browser from doing work for pixels that are already scrolled past.
    m.loading = "lazy";
    m.decoding = "async";
  } else {
    m.controls = true;
  }
  m.preload = "metadata";
  m.src = url;
  wrap.appendChild(m);
  host.appendChild(wrap);
}

function renderPdf(host, url) {
  const wrap = document.createElement("div");
  wrap.className = "media";
  const e = document.createElement("embed");
  e.src = url;
  e.type = "application/pdf";
  wrap.appendChild(e);
  host.appendChild(wrap);
}

// ---------------------------------------------------------- header row ----

function renderHead(path) {
  ui.crumb.replaceChildren();
  const root = document.createElement("span");
  root.className = "root-crumb";
  root.textContent = "~";
  ui.crumb.appendChild(root);

  const segs = path.split("/").filter(Boolean);
  let acc = "";
  segs.forEach((seg, i) => {
    acc = acc ? acc + "/" + seg : seg;
    const sep = document.createElement("span");
    sep.className = "sep";
    sep.textContent = "/";
    const last = i === segs.length - 1;
    const link = document.createElement(last ? "b" : "span");
    link.textContent = seg;
    if (!last) {
      // Ancestors are links, and the tooltip says where they lead so a long
      // path is still unambiguous when the labels are truncated.
      link.dataset.link = "";
      link.title = acc;
      link.addEventListener("click", () => show(acc));
    }
    ui.crumb.append(sep, link);
  });

  const node = nodes.get(path);
  const isDir = path === "" || !!(node && node.isDir);
  ui.meta.replaceChildren();
  const put = (text) => {
    const s = document.createElement("span");
    s.textContent = text;
    ui.meta.appendChild(s);
  };
  const dot = () => {
    const s = document.createElement("span");
    s.className = "dot";
    s.textContent = "·";
    ui.meta.appendChild(s);
  };
  if (isDir) {
    put("directory");
  } else {
    put(humanSize(node ? node.size : 0));
    dot();
    put(humanTime(node ? node.mtime : 0));
    // The type is what tells a reader whether the preview below is text, an
    // image or a blob; without it the pane gives no clue.
    const kind = fileKind(nameOf(path));
    if (kind) {
      dot();
      put(kind);
    }
  }

  ui.actions.replaceChildren();
  if (!isDir) ui.actions.append(copyBtn(path), rawBtn(path), dlBtn(path));

  // When the path does not fit, the overflow clips silently at the start edge;
  // a left fade says "there is more above" instead of a hard mid-word cut.
  ui.crumb.classList.toggle("clipped", ui.crumb.scrollWidth > ui.crumb.clientWidth);
}

function copyBtn(path) {
  const b = document.createElement("button");
  b.className = "btn";
  b.innerHTML = ICON.copy + '<span class="label">Copy curl</span>';
  b.title = "Copy the curl command for this file";
  b.addEventListener("click", async () => {
    const cmd = "curl -fsSL " + absUrl(path) + (auth() ? "?" + auth() : "");
    try {
      await navigator.clipboard.writeText(cmd);
      b.classList.add("done");
      setTimeout(() => b.classList.remove("done"), 1200);
    } catch {
      // Clipboard access can be denied; show the command so it can be copied.
      window.prompt("Copy command", cmd);
    }
  });
  return b;
}

function rawBtn(path) {
  const a = document.createElement("a");
  a.className = "btn";
  a.href = absUrl(path) + (auth() ? "?" + auth() : "");
  a.target = "_blank";
  a.rel = "noopener";
  a.innerHTML = ICON.link + '<span class="label">Raw</span>';
  a.title = "Open the file's own URL";
  return a;
}

function dlBtn(path) {
  const a = document.createElement("a");
  a.className = "btn";
  a.href = absUrl(path) + "?dl=1" + (auth() ? "&" + auth() : "");
  a.innerHTML = ICON.download + '<span class="label">Download</span>';
  a.title = "Download";
  return a;
}
// ------------------------------------------------------------- drawer ----
// Below the breakpoint the tree is a slide-over drawer rather than a column:
// the two-column grid squeezes the preview to a sliver at phone widths. The
// state lives on the document element so the CSS can key off it, and that one
// attribute is the source of truth for the button, the scrim and Escape.
function setDrawer(open, force) {
  // There is no drawer above the breakpoint, so the button is inert there.
  // force is only how a resize back to desktop clears a phone-only state.
  if (!narrow.matches && !force) return;
  document.documentElement.dataset.drawer = open ? "open" : "closed";
  ui.drawer.setAttribute("aria-expanded", open ? "true" : "false");
  ui.scrim.hidden = !open;
  // Opening puts the caret in the filter: filtering is what the drawer is for
  // on a phone, and it saves a second tap on the way to any file.
  if (open) ui.filter.focus();
  // Dismissing with the keyboard would strand focus on a control that is now
  // off screen, so it goes back to the button that opened the drawer.
  else if (ui.sidebar.contains(document.activeElement)) ui.drawer.focus();
}
// The trigger toggles: while the drawer is open the scrim covers the pane but
// not the topbar, so this is the only always-reachable way back to the tree's
// siblings, and a trigger that only opens would strand the user on it.
ui.drawer.addEventListener("click", () =>
  setDrawer(document.documentElement.dataset.drawer !== "open"));
ui.scrim.addEventListener("click", () => setDrawer(false));
// The up chevron navigates to the parent directory — the same move the
// breadcrumb's ancestors offer, one tap from the topbar. show() then closes
// the drawer for us on a phone, so this button needs no drawer logic of its
// own; a second close-the-drawer button read as broken whenever the drawer
// was already closed, which is the state it sits in most of the time.
ui.back.addEventListener("click", () => {
  if (selected) show(parentOf(selected));
});
// Tapping a file navigates, and the preview is what the user came for, so the
// drawer closes at once — show() would close it too, but only after the fetch.
// Tapping a directory only folds or unfolds it; closing there made the tree
// unusable for browsing, one press per directory.
ui.tree.addEventListener("click", (e) => {
  if (e.target.closest(".row.file")) setDrawer(false);
});
// ------------------------------------------------------------- theme ----
// Light and dark are two token palettes in the CSS; which one applies is a
// data-theme attribute on the document element. Until this module sets it, no
// attribute exists and the palettes follow the OS preference, so the first
// paint is always right and there is no flash when the theme is merely
// following the system. An explicit choice is stored and wins until cleared.
const THEME_KEY = "dirserve-theme";
const sysDark = matchMedia("(prefers-color-scheme: dark)");
let themeChoice = null; // null means "follow the system"
try {
  const saved = localStorage.getItem(THEME_KEY);
  if (saved === "light" || saved === "dark") themeChoice = saved;
} catch {
  // Storage can be unavailable (private mode, hardened browsers); the toggle
  // still works for the session, it just does not persist.
}

function applyTheme() {
  const theme = themeChoice ?? (sysDark.matches ? "dark" : "light");
  document.documentElement.dataset.theme = theme;
  ui.theme.setAttribute(
    "aria-label",
    theme === "dark" ? "Switch to light theme" : "Switch to dark theme",
  );
}

ui.theme.addEventListener("click", () => {
  themeChoice = document.documentElement.dataset.theme === "dark" ? "light" : "dark";
  try {
    localStorage.setItem(THEME_KEY, themeChoice);
  } catch {}
  applyTheme();
});
// While following the system, a mid-session flip of the OS setting updates.
sysDark.addEventListener("change", () => {
  if (!themeChoice) applyTheme();
});
applyTheme();

// The "/" hint and the clear affordance are the filter's two ends: one opens
// it from the keyboard, the other empties it where a phone has no Escape.
ui.filter.addEventListener("input", () => {
  ui.filterClear.hidden = !ui.filter.value;
});
ui.filterClear.addEventListener("click", () => {
  ui.filter.value = "";
  ui.filterClear.hidden = true;
  renderTree();
  ui.filter.focus();
});

// Leaving the phone range must not strand a phone-only drawer on desktop.
narrow.addEventListener("change", (e) => { if (!e.matches) setDrawer(false, true); });
// ------------------------------------------------------------ keyboard ----

const visibleRows = () =>
  Array.from(ui.tree.querySelectorAll(".row.dir, .row.file"));

document.addEventListener("keydown", (e) => {
  if (e.key === "/" && document.activeElement !== ui.filter) {
    e.preventDefault();
    ui.filter.focus();
    ui.filter.select();
    return;
  }
  if (e.key === "Escape") {
    // The drawer is the topmost layer, so it takes the first Escape and the
    // filter is left for the next time it is opened.
    if (document.documentElement.dataset.drawer === "open") {
      setDrawer(false);
      return;
    }
    if (document.activeElement === ui.filter) {
      ui.filter.value = "";
      renderTree();
      ui.filter.blur();
    }
    return;
  }
  if (e.key !== "ArrowDown" && e.key !== "ArrowUp" && e.key !== "Enter") return;
  // While the filter has text, the arrows belong to typing, not to the tree.
  if (document.activeElement === ui.filter && ui.filter.value) return;

  const rows = visibleRows();
  if (!rows.length) return;
  let i = rows.findIndex((r) => r.classList.contains("selected"));

  if (e.key === "Enter") {
    if (i < 0) return;
    e.preventDefault();
    const node = nodes.get(rows[i].dataset.path);
    if (node.isDir) toggle(node);
    else show(node.path);
    return;
  }
  // Arrows move the selection only. They must not expand: the tree re-renders
  // from the model on every structural change, which would drop the selection
  // that was just set. Enter is what opens a folder or previews a file.
  e.preventDefault();
  i = e.key === "ArrowDown" ? Math.min(i + 1, rows.length - 1) : Math.max(i - 1, 0);
  if (i < 0) i = 0;
  selected = rows[i].dataset.path;
  renderTree();
  const sel = ui.tree.querySelector(".row.selected");
  if (sel) sel.scrollIntoView({ block: "nearest" });
});

// Own navigations use pushState/replaceState, which fire neither event, so
// these two only ever fire for a real URL change: the back button, a hand-
// edited hash, or a pasted link. Both are handled because a hash assignment
// fires hashchange but not popstate, while history navigation fires both;
// lastRendered keeps that from rendering twice.
let lastRendered = null;
function onURLChanged() {
  const next = hashPath();
  if (next === lastRendered) return;
  lastRendered = next;
  show(next, true).catch(() => {
    ui.tree.replaceChildren(noteRow("cannot read directory", 0));
  });
}
window.addEventListener("popstate", onURLChanged);
window.addEventListener("hashchange", onURLChanged);

ui.filter.addEventListener("input", renderTree);

const initial = hashPath();
lastRendered = initial;
show(initial, true).catch(() => {
  ui.tree.replaceChildren(noteRow("cannot read directory", 0));
});

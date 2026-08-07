"use strict";

/* mgit 뷰어 — 데이터는 전부 로컬 API 에서 온다. 레인 배치는 서버(internal/graph)가
   계산해서 내려주므로 여기서는 그리기만 한다. */

const R = 32, LW = 16, PAD = 16;   // 행 높이 / 레인 간격 / 좌측 여백
const $ = s => document.querySelector(s);
const clamp = (v, lo, hi) => Math.max(lo, Math.min(hi, v));

const state = {
  repo: null,
  commits: [],
  byId: new Map(),
  layout: {nodes: [], edges: [], width: 0},
  noteCounts: {},
  notes: [],
  sel: null,
  detail: null,      // /api/commit/{sha} — 본문 + 파일 통계
  diff: null,
  view: "graph",
  showAll: true,
  composer: null,    // {line, path} | "commit"
  flashNote: null,
  collapsed: new Set(),
  seq: -1,
  notesRev: undefined,
};

/* ── API ────────────────────────────────────────────────────────── */
async function api(path, opts) {
  const res = await fetch(path, opts);
  if (res.status === 204) return null;
  const ct = res.headers.get("content-type") || "";
  const body = ct.includes("json") ? await res.json() : await res.text();
  if (!res.ok) throw new Error((body && body.error) || String(body) || res.statusText);
  return body;
}

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}
const baseOf = p => (p ? p.split("/").pop() : "");
const dirOf = p => {
  const i = (p || "").lastIndexOf("/");
  return i < 0 ? "" : p.slice(0, i + 1);
};

/* ── 그래프 ─────────────────────────────────────────────────────── */
const rowY = r => r * R + R / 2;
const laneX = l => PAD + l * LW;
const laneVar = l => `var(--l${l % 6})`;

// 자식에서 부모로 내려가는 선. 오른쪽으로 벌어지면 자식 바로 아래에서 휘고,
// 왼쪽으로 합류하면 부모 바로 위에서 휜다. SourceTree 가 그리는 모양이다.
function edgePath(e) {
  const x1 = laneX(e.fromLane), y1 = rowY(e.fromRow);
  const x2 = laneX(e.toLane), y2 = rowY(e.toRow);
  if (x1 === x2) return `M${x1},${y1} L${x2},${y2}`;
  const ya = x2 > x1 ? y1 + R * 0.15 : y2 - R * 0.85;
  const yb = x2 > x1 ? y1 + R * 0.85 : y2 - R * 0.15;
  return `M${x1},${y1} L${x1},${ya} C${x1},${yb} ${x2},${ya} ${x2},${yb} L${x2},${y2}`;
}

const graphWidth = () => PAD * 2 + Math.max(0, state.layout.width - 1) * LW;

function drawGraph() {
  const svg = $("#lanes");
  const gw = graphWidth(), h = state.commits.length * R;
  svg.setAttribute("width", gw + 8);
  svg.setAttribute("height", h);
  svg.setAttribute("viewBox", `0 0 ${gw + 8} ${h}`);

  let s = "";
  for (const e of state.layout.edges) {
    s += `<path d="${edgePath(e)}" fill="none" stroke="${laneVar(e.toLane)}" stroke-width="1.8"`
       + (e.dangling ? ` stroke-dasharray="3 3" opacity=".45"` : ` opacity=".9"`) + `/>`;
  }
  for (const n of state.layout.nodes) {
    const c = state.byId.get(n.sha);
    const merge = c && c.parents && c.parents.length > 1;
    const x = laneX(n.lane), y = rowY(n.row);
    s += `<circle cx="${x}" cy="${y}" r="${merge ? 3.6 : 4.8}"`
       + ` fill="${merge ? "var(--panel)" : laneVar(n.lane)}"`
       + ` stroke="${laneVar(n.lane)}" stroke-width="${merge ? 2 : 1.5}"/>`;
    if (state.noteCounts[n.sha])
      s += `<circle cx="${x}" cy="${y}" r="8.5" fill="none" stroke="var(--accent-solid)" stroke-width="1.4" opacity=".9"/>`;
  }
  svg.innerHTML = s;
}

function refChip(ref) {
  const chip = el("span", "chip " + ref.kind);
  const mark = ref.kind === "tag" ? "⌂ " : ref.kind === "head" ? "➤ " : "";
  chip.textContent = mark + ref.name;
  chip.title = ref.name;
  return chip;
}

function fmtDate(iso) {
  if (!iso) return "";
  const d = new Date(iso);
  if (isNaN(d)) return "";
  const p = n => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

function drawRows() {
  const box = $("#rows");
  box.textContent = "";
  const gw = graphWidth();

  for (const c of state.commits) {
    const row = el("div", "crow" + (c.parents && c.parents.length > 1 ? " merge" : "")
                            + (c.sha === state.sel ? " sel" : ""));
    row.tabIndex = 0;
    row.dataset.sha = c.sha;
    row.setAttribute("role", "button");
    row.setAttribute("aria-label", `${c.short} ${c.subject}`);

    const sp = el("div", "gspace");
    sp.style.width = (gw + 8) + "px";
    row.appendChild(sp);

    const nd = el("div", "notedot");
    const cnt = state.noteCounts[c.sha] || 0;
    if (cnt) { const b = el("b", "", String(cnt)); b.title = `${cnt}개 메모`; nd.appendChild(b); }
    row.appendChild(nd);

    const subj = el("div", "csubj");
    for (const ref of c.refs || []) subj.appendChild(refChip(ref));
    subj.appendChild(document.createTextNode(c.subject));
    row.appendChild(subj);

    const meta = el("div", "cmeta");
    for (const [cls, val] of [["au", c.author], ["dt", fmtDate(c.date)], ["sh", c.short]]) {
      meta.appendChild(el("span", cls, val || ""));
    }
    row.appendChild(meta);
    box.appendChild(row);
  }
  $("#graphmeta").textContent = `${state.commits.length} commits · ${state.layout.width} lanes`;
}

/* ── 그래프 하단 패널 ───────────────────────────────────────────── */
// SourceTree 처럼 목록에서 커밋을 고르면 아래에 요약이 뜬다. 상세 뷰로
// 넘어가지 않고도 "이 커밋이 뭘 건드렸나"를 훑기 위한 화면이다.
function drawGraphDetail() {
  const info = $("#gdinfo"), files = $("#gdfiles");
  info.textContent = "";
  files.textContent = "";

  const c = state.byId.get(state.sel);
  if (!c) { info.appendChild(el("div", "emptyish", "커밋을 선택하세요.")); return; }

  const subj = el("div", "gsubj");
  for (const ref of c.refs || []) subj.appendChild(refChip(ref));
  subj.appendChild(document.createTextNode(c.subject));
  info.appendChild(subj);

  const dl = document.createElement("dl");
  const rows = [
    ["커밋", c.sha],
    ["상위 항목", (c.parents || []).map(p => p.slice(0, 9)).join("  ") || "—"],
    ["작성자", c.author],
    ["날짜", fmtDate(c.date)],
  ];
  for (const [k, v] of rows) {
    dl.appendChild(el("dt", "", k));
    dl.appendChild(el("dd", "", v));
  }
  info.appendChild(dl);

  // 커밋 본문에서 제목 줄을 뺀 나머지만 보여준다.
  if (state.detail && state.detail.body) {
    const rest = state.detail.body.split("\n").slice(1).join("\n").trim();
    if (rest) info.appendChild(el("div", "gbody", rest));
  }

  const stats = (state.detail && state.detail.stats) || [];
  const head = el("div", "gf-head");
  head.appendChild(el("span", "", state.detail ? `${stats.length}개 파일 변경` : "불러오는 중…"));
  files.appendChild(head);

  for (const st of stats) {
    const row = el("div", "gf-row");
    row.tabIndex = 0;
    row.setAttribute("role", "button");
    const fp = el("span", "fp", st.path);
    fp.title = st.path;
    const s = el("span", "st");
    if (st.bin) {
      s.appendChild(el("span", "", "binary"));
    } else {
      s.appendChild(el("span", "a", `+${st.add}`));
      s.appendChild(document.createTextNode(" "));
      s.appendChild(el("span", "d", `−${st.del}`));
    }
    row.appendChild(fp);
    row.appendChild(s);
    const go = () => { setView("detail"); scrollToFile(st.path); };
    row.onclick = go;
    row.onkeydown = ev => { if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); go(); } };
    files.appendChild(row);
  }
}

function scrollToFile(path) {
  requestAnimationFrame(() => {
    const card = document.querySelector(`.filecard[data-path="${CSS.escape(path)}"]`);
    if (card) card.scrollIntoView({block: "start", behavior: "smooth"});
  });
}

/* ── 상세 뷰 ────────────────────────────────────────────────────── */
function drawDetail() {
  const c = state.byId.get(state.sel);
  const box = $("#cdetail");
  box.textContent = "";
  if (!c) return;

  const subj = el("div", "subj");
  for (const ref of c.refs || []) subj.appendChild(refChip(ref));
  subj.appendChild(document.createTextNode(c.subject));
  box.appendChild(subj);

  const l2 = el("div", "line2");
  const parents = (c.parents || []).map(p => p.slice(0, 9)).join(" ") || "—";
  for (const [cls, v] of [["sha", c.short], ["au", c.author], ["dt", fmtDate(c.date)],
                          ["par", "parents: " + parents]]) {
    l2.appendChild(el("span", cls, v));
  }
  box.appendChild(l2);
  $("#diffmeta").textContent = (c.parents || []).length > 1 ? "merge commit · 첫 부모 기준" : "";
}

function notesForLine(path, line) {
  return state.notes.filter(n =>
    n.kind === "line" && n.anchor.commit === state.sel &&
    n.anchor.path === path && n.anchor.line === line);
}

function drawDiff() {
  const body = $("#diffbody");
  body.textContent = "";

  if (state.diff === null) { body.appendChild(el("div", "emptyish", "불러오는 중…")); return; }
  if (state.diff.error) { body.appendChild(el("div", "err", state.diff.error)); return; }

  const files = state.diff.files || [];
  if (!files.length) { body.appendChild(el("div", "emptyish", "변경된 파일이 없습니다.")); return; }

  for (const f of files) {
    body.appendChild(fileCard(f));
  }
  state.flashNote = null;
}

function fileCard(f) {
  const card = el("div", "filecard");
  card.dataset.path = f.path;
  if (state.collapsed.has(f.path)) card.classList.add("collapsed");

  const head = el("div", "filehead");
  head.appendChild(el("span", "fold", "▼"));
  const fp = el("span", "fp");
  const dir = dirOf(f.oldPath && f.oldPath !== f.path ? f.path : f.path);
  if (dir) fp.appendChild(el("span", "dir", dir));
  fp.appendChild(document.createTextNode(baseOf(f.path)));
  fp.title = f.oldPath && f.oldPath !== f.path ? `${f.oldPath} → ${f.path}` : f.path;
  head.appendChild(fp);
  if (f.oldPath && f.oldPath !== f.path) {
    head.appendChild(el("span", "chip branch", "renamed"));
  }
  const st = el("span", "st");
  st.appendChild(el("span", "a", `+${f.add}`));
  st.appendChild(document.createTextNode(" "));
  st.appendChild(el("span", "d", `−${f.del}`));
  head.appendChild(st);
  head.onclick = () => {
    if (state.collapsed.has(f.path)) state.collapsed.delete(f.path);
    else state.collapsed.add(f.path);
    card.classList.toggle("collapsed");
  };
  card.appendChild(head);

  const fbody = el("div", "filebody");
  if (f.binary) {
    fbody.appendChild(el("div", "emptyish", "바이너리 파일"));
    card.appendChild(fbody);
    return card;
  }

  const lang = HL.langOf(f.path);
  for (const hk of f.hunks || []) {
    const sec = el("div", "hunk");
    sec.appendChild(el("div", "hunkhead", hk.header));

    // 블록 주석 상태는 헝크 안에서만 이어진다. 헝크 사이는 코드가 건너뛰므로
    // 이어 붙이면 오히려 틀린다. 추가/삭제 줄이 섞이니 양쪽 상태를 따로 둔다.
    let sNew = {block: false}, sOld = {block: false};

    for (const L of hk.lines || []) {
      const anchor = L.newNo || 0;
      const line = el("div", "dline" + (L.kind === "add" ? " add" : L.kind === "del" ? " del" : ""));
      const mine = anchor ? notesForLine(f.path, anchor) : [];
      if (anchor) {
        line.dataset.line = anchor;
        if (mine.length) line.classList.add("noted");
      }

      const gut = el("div", "gut");
      gut.appendChild(el("i", "", L.oldNo ? String(L.oldNo) : ""));
      gut.appendChild(el("i", "", L.newNo ? String(L.newNo) : ""));
      if (anchor) {
        const b = el("i", "addbtn", "✎");
        b.tabIndex = 0;
        b.setAttribute("role", "button");
        b.title = `${anchor}번 줄에 메모 달기`;
        b.setAttribute("aria-label", b.title);
        const open = ev => {
          ev.preventDefault(); ev.stopPropagation();
          state.composer = {line: anchor, path: f.path};
          state.flashNote = null;
          drawDiff();
          const ta = $("#composer-ta"); if (ta) ta.focus();
        };
        b.addEventListener("click", open);
        b.addEventListener("keydown", ev => { if (ev.key === "Enter" || ev.key === " ") open(ev); });
        gut.appendChild(b);
      }
      line.appendChild(gut);
      line.appendChild(el("div", "sig", L.kind === "add" ? "+" : L.kind === "del" ? "−" : ""));

      const txt = el("div", "txt");
      const src = L.text || "";
      if (lang) {
        // 컨텍스트 줄은 양쪽 상태를 모두 전진시킨다.
        if (L.kind === "del") {
          const r = HL.highlight(src, lang, sOld); sOld = r.state; txt.innerHTML = r.html || " ";
        } else if (L.kind === "add") {
          const r = HL.highlight(src, lang, sNew); sNew = r.state; txt.innerHTML = r.html || " ";
        } else {
          const r = HL.highlight(src, lang, sNew);
          sNew = r.state;
          sOld = HL.highlight(src, lang, sOld).state;
          txt.innerHTML = r.html || " ";
        }
      } else {
        txt.textContent = src || " ";
      }
      line.appendChild(txt);
      sec.appendChild(line);

      for (const n of mine) sec.appendChild(inlineNote(n));
      if (state.composer && state.composer !== "commit" &&
          state.composer.line === anchor && state.composer.path === f.path) {
        sec.appendChild(composer("line"));
      }
    }
    fbody.appendChild(sec);
  }
  card.appendChild(fbody);
  return card;
}

function inlineNote(n) {
  const box = el("div", "inote " + n.status);
  box.dataset.id = n.id;
  if (state.flashNote === n.id) box.classList.add("flash");

  const ih = el("div", "ih");
  ih.appendChild(el("span", "nid", n.id));
  ih.appendChild(el("span", "nst", n.status));
  const at = el("span", "iat", `${baseOf(n.anchor.path)}:${n.anchor.line}`);
  at.title = `${n.anchor.path}:${n.anchor.line}`;
  ih.appendChild(at);
  const x = el("button", "nx", "✕");
  x.type = "button"; x.title = "메모 삭제";
  x.setAttribute("aria-label", `${n.id} 삭제`);
  x.onclick = ev => { ev.stopPropagation(); removeNote(n); };
  ih.appendChild(x);
  box.appendChild(ih);

  box.appendChild(el("div", "ibody", n.body));
  if (n.resolution) box.appendChild(el("div", "ires", "→ " + n.resolution));

  const act = el("div", "iact");
  const tg = el("button", "btn sm" + (n.status === "open" ? " pri" : ""),
                n.status === "open" ? "완료 처리" : "다시 열기");
  tg.type = "button";
  tg.onclick = ev => { ev.stopPropagation(); toggleNote(n); };
  act.appendChild(tg);
  box.appendChild(act);
  return box;
}

function composer(kind) {
  const box = el("div", "composer" + (kind === "commit" ? " commit" : ""));
  const ta = document.createElement("textarea");
  ta.id = "composer-ta";
  ta.placeholder = kind === "commit"
    ? "이 커밋 전체에 대한 메모…"
    : "에이전트가 읽을 메모… (예: 이 조건에 null 들어오면?)";
  box.appendChild(ta);

  const cf = el("div", "cf");
  const c = state.byId.get(state.sel);
  cf.appendChild(el("span", "at", kind === "commit"
    ? `${c ? c.short : ""} · 커밋 전체`
    : `${c ? c.short : ""} · ${baseOf(state.composer.path)}:${state.composer.line}`));
  const hint = el("span", "at", "Ctrl+Enter 저장");
  hint.style.flex = "none"; hint.style.marginRight = "0";
  cf.appendChild(hint);

  const cancel = el("button", "btn", "취소");
  cancel.type = "button";
  cancel.onclick = () => { state.composer = null; render(); };
  const save = el("button", "btn pri", "메모 저장");
  save.type = "button";
  save.onclick = async () => {
    const v = ta.value.trim();
    if (!v) { ta.focus(); return; }
    save.disabled = true;
    try { await createNote(kind, v); }
    catch (e) { toast("메모 저장 실패: " + e.message); save.disabled = false; }
  };
  ta.addEventListener("keydown", ev => {
    if (ev.key === "Enter" && (ev.ctrlKey || ev.metaKey)) { ev.preventDefault(); save.click(); }
    if (ev.key === "Escape") { ev.preventDefault(); cancel.click(); }
  });
  cf.appendChild(cancel); cf.appendChild(save);
  box.appendChild(cf);
  return box;
}

/* ── 메모 레일 ──────────────────────────────────────────────────── */
const visibleNotes = () =>
  state.showAll ? state.notes : state.notes.filter(n => n.anchor.commit === state.sel);

function drawNotes() {
  const pane = $("#p-notes");
  pane.textContent = "";

  const grp = el("div", "notegrp");
  const c = state.byId.get(state.sel);
  grp.appendChild(el("span", "", state.showAll ? "레포 전체" : "이 커밋 " + (c ? c.short : "")));
  grp.appendChild(el("span", "ln"));
  pane.appendChild(grp);

  if (state.composer === "commit") pane.appendChild(composer("commit"));

  const list = visibleNotes();
  if (!list.length) {
    pane.appendChild(el("div", "emptyish", "메모 없음.\ndiff 라인 왼쪽 ✎ 를 눌러 남겨보세요."));
  }

  for (const n of list) {
    const box = el("div", "note " + n.status);
    box.tabIndex = 0;
    box.setAttribute("role", "button");
    box.setAttribute("aria-label", `${n.id} 위치로 이동`);

    const nh = el("div", "nh");
    nh.appendChild(el("span", "nid", n.id));
    nh.appendChild(el("span", "nst", n.status));
    const x = el("button", "nx", "✕");
    x.type = "button"; x.title = "메모 삭제";
    x.setAttribute("aria-label", `${n.id} 삭제`);
    x.onclick = ev => { ev.stopPropagation(); removeNote(n); };
    nh.appendChild(x);
    box.appendChild(nh);

    const short = (n.anchor.commit || "").slice(0, 9);
    box.appendChild(el("div", "nloc", n.kind === "line"
      ? `${short} · ${baseOf(n.anchor.path)}:${n.anchor.line}`
      : `${short} · 커밋 전체`));
    box.appendChild(el("div", "nbody", n.body));
    if (n.resolution) box.appendChild(el("div", "nres", "→ " + n.resolution));

    const act = el("div", "nact");
    const tg = el("button", "btn sm" + (n.status === "open" ? " pri" : ""),
                  n.status === "open" ? "완료 처리" : "다시 열기");
    tg.type = "button";
    tg.onclick = ev => { ev.stopPropagation(); toggleNote(n); };
    act.appendChild(tg);
    box.appendChild(act);

    box.onclick = () => jumpTo(n);
    box.onkeydown = ev => { if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); jumpTo(n); } };
    pane.appendChild(box);
  }

  const open = state.notes.filter(n => n.status === "open").length;
  $("#notecnt").textContent = `open ${open} / total ${state.notes.length}`;
  $("#filterbtn").textContent = state.showAll ? "이 커밋만" : "전체 보기";
  $("#tabn").textContent = list.length ? `(${list.length})` : "";
}

async function drawCli() {
  try {
    const md = await api("/api/export?status=open");
    $("#cliout").textContent = "$ mgit note export --status open\n\n" + md;
  } catch (e) {
    $("#cliout").textContent = "export 실패: " + e.message;
  }
}

/* ── 동작 ───────────────────────────────────────────────────────── */
async function createNote(kind, body) {
  const payload = kind === "commit"
    ? {kind: "commit", commit: state.sel, body}
    : {kind: "line", commit: state.sel, path: state.composer.path, line: state.composer.line, body};
  await api("/api/notes", {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify(payload),
  });
  state.composer = null;
  await reloadNotes();
  selectTab("tab-notes");
  toast("메모를 저장했습니다");
}

async function toggleNote(n) {
  const next = n.status === "open" ? "done" : "open";
  try {
    await api(`/api/notes/${n.id}`, {
      method: "PATCH",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify(next === "done"
        ? {status: next, resolution: n.resolution || "에이전트 처리 대기"}
        : {status: next}),
    });
    await reloadNotes();
  } catch (e) { toast("상태 변경 실패: " + e.message); }
}

async function removeNote(n) {
  try {
    await api(`/api/notes/${n.id}`, {method: "DELETE"});
    await reloadNotes();
  } catch (e) { toast("삭제 실패: " + e.message); }
}

async function reloadNotes() {
  state.notes = await api("/api/notes?status=all") || [];
  state.noteCounts = {};
  for (const n of state.notes) {
    state.noteCounts[n.anchor.commit] = (state.noteCounts[n.anchor.commit] || 0) + 1;
  }
  try {
    const st = await api("/api/state");
    state.notesRev = st.notesRev;   // 내 변경을 남의 변경으로 오인하지 않도록 맞춰둔다
  } catch (_) {}
  render();
  if ($("#tab-cli").getAttribute("aria-selected") === "true") drawCli();
}

function jumpTo(n) {
  if (n.anchor.commit !== state.sel) select(n.anchor.commit);
  setView("detail");
  if (n.kind !== "line") return;
  state.flashNote = n.id;
  drawDiff();
  const t = document.querySelector(`.inote[data-id="${n.id}"]`);
  if (t) t.scrollIntoView({block: "center", behavior: "smooth"});
}

async function select(sha) {
  if (!sha) return;
  state.sel = sha;
  state.composer = null;
  state.diff = null;
  state.detail = null;
  state.collapsed.clear();
  render();
  updateCmd();
  $("#dscroll").scrollTop = 0;

  try {
    const [detail, diff] = await Promise.all([
      api(`/api/commit/${sha}`),
      api(`/api/diff/${sha}`),
    ]);
    if (state.sel !== sha) return;   // 그 사이 다른 커밋을 골랐다
    state.detail = detail;
    state.diff = diff;
  } catch (e) {
    if (state.sel !== sha) return;
    state.diff = {error: "불러오지 못했습니다: " + e.message};
  }
  drawDiff();
  drawGraphDetail();
}

function setView(v) {
  if (state.view === v) return;
  state.view = v;
  $("#app").dataset.view = v;
  $("#v-graph").setAttribute("aria-pressed", String(v === "graph"));
  $("#v-detail").setAttribute("aria-pressed", String(v === "detail"));
  state.showAll = (v === "graph");
  state.composer = null;
  drawNotes();
  updateCmd();
}

function updateCmd() {
  const name = state.repo ? state.repo.name : ".";
  const c = state.byId.get(state.sel);
  $("#cmd").textContent = "mgit " + name + (state.view === "detail" && c ? " -c " + c.short : "");
}

function selectTab(id) {
  document.querySelectorAll(".tab").forEach(t => t.setAttribute("aria-selected", "false"));
  document.querySelectorAll(".tabpane").forEach(p => p.classList.remove("on"));
  const t = document.getElementById(id);
  t.setAttribute("aria-selected", "true");
  document.getElementById(t.getAttribute("aria-controls")).classList.add("on");
  if (id === "tab-cli") drawCli();
}

let toastTimer = null;
function toast(msg) {
  const t = $("#toast");
  t.textContent = msg;
  t.classList.add("on");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.remove("on"), 2200);
}

function render() {
  drawGraph(); drawRows(); drawDetail(); drawDiff(); drawGraphDetail(); drawNotes();
}

/* ── 테마 ───────────────────────────────────────────────────────── */
// 자동(OS 설정) → 라이트 → 다크 순으로 돈다. 자동이 기본이라 처음 켰을 때
// 시스템 설정과 어긋나지 않는다.
const THEME_KEY = "mgit.theme";
const THEMES = ["auto", "light", "dark"];
const THEME_ICON = {auto: "◐", light: "☀", dark: "☾"};
const THEME_NAME = {auto: "자동", light: "라이트", dark: "다크"};

function currentTheme() {
  const t = document.documentElement.dataset.theme;
  return t === "light" || t === "dark" ? t : "auto";
}

function applyTheme(t) {
  if (t === "auto") {
    delete document.documentElement.dataset.theme;
    try { localStorage.removeItem(THEME_KEY); } catch (_) {}
  } else {
    document.documentElement.dataset.theme = t;
    try { localStorage.setItem(THEME_KEY, t); } catch (_) {}
  }
  const btn = $("#theme");
  btn.textContent = THEME_ICON[t];
  btn.title = `테마: ${THEME_NAME[t]} (눌러서 전환)`;
  btn.setAttribute("aria-label", btn.title);
}

function initTheme() {
  applyTheme(currentTheme());
  $("#theme").onclick = () => {
    const next = THEMES[(THEMES.indexOf(currentTheme()) + 1) % THEMES.length];
    applyTheme(next);
    toast(`테마: ${THEME_NAME[next]}`);
  };
}

/* ── 하단 패널 크기 조절 ────────────────────────────────────────── */
const GD_KEY = "mgit.gdetail.height";
const GD_OPEN_KEY = "mgit.gdetail.open";

function setGdetail(open) {
  $("#app").dataset.gdetail = open ? "on" : "off";
  const btn = $("#togglegd");
  btn.textContent = (open ? "▼" : "▲") + " 하단 패널";
  btn.setAttribute("aria-expanded", String(open));
  try { localStorage.setItem(GD_OPEN_KEY, open ? "1" : "0"); } catch (_) {}
}

function initSplitter() {
  const sp = $("#gsplit"), gd = $("#gdetail");

  setGdetail(localStorage.getItem(GD_OPEN_KEY) !== "0");
  $("#togglegd").onclick = () => setGdetail($("#app").dataset.gdetail === "off");
  // 구분선 더블클릭으로도 접었다 폈다 한다.
  sp.addEventListener("dblclick", () => setGdetail($("#app").dataset.gdetail === "off"));
  // 접힌 상태에서는 한 번만 눌러도 펼쳐진다.
  sp.addEventListener("click", () => {
    if ($("#app").dataset.gdetail === "off") setGdetail(true);
  });
  const limit = h => clamp(h, 120, Math.max(160, window.innerHeight - 260));

  const saved = parseInt(localStorage.getItem(GD_KEY) || "", 10);
  if (!isNaN(saved)) gd.style.height = limit(saved) + "px";

  let dragging = false, startY = 0, startH = 0;
  const persist = () => localStorage.setItem(GD_KEY, String(gd.offsetHeight));

  sp.addEventListener("mousedown", e => {
    if ($("#app").dataset.gdetail === "off") return;   // 접혀 있으면 클릭으로 펼치기만
    dragging = true;
    startY = e.clientY;
    startH = gd.offsetHeight;
    sp.classList.add("dragging");
    document.body.classList.add("resizing");
    e.preventDefault();
  });
  window.addEventListener("mousemove", e => {
    if (!dragging) return;
    gd.style.height = limit(startH - (e.clientY - startY)) + "px";
  });
  window.addEventListener("mouseup", () => {
    if (!dragging) return;
    dragging = false;
    sp.classList.remove("dragging");
    document.body.classList.remove("resizing");
    persist();
  });
  sp.addEventListener("keydown", e => {
    const step = e.shiftKey ? 48 : 14;
    if (e.key === "ArrowUp") { e.preventDefault(); gd.style.height = limit(gd.offsetHeight + step) + "px"; persist(); }
    if (e.key === "ArrowDown") { e.preventDefault(); gd.style.height = limit(gd.offsetHeight - step) + "px"; persist(); }
  });
  window.addEventListener("resize", () => { gd.style.height = limit(gd.offsetHeight) + "px"; });
}

/* ── 폴링 ───────────────────────────────────────────────────────── */
// 이 화면 밖에서 일어나는 변화 두 가지를 따라간다.
//   1. 두 번째 `mgit . -c <sha>` 실행이 바꾼 target
//   2. 에이전트가 `mgit note done` 으로 고친 메모 파일
async function pollState() {
  try {
    const st = await api("/api/state");

    if (st.notesRev !== state.notesRev) {
      const first = state.notesRev === undefined;
      state.notesRev = st.notesRev;
      if (!first) { await reloadNotes(); toast("메모가 갱신되었습니다"); }
    }

    if (st.seq !== state.seq) {
      const first = state.seq < 0;
      state.seq = st.seq;
      if (st.target && st.target !== state.sel) {
        await select(st.target);
        if (!first) { setView("detail"); toast("커밋으로 이동했습니다"); }
      }
    }
  } catch (_) { /* 서버가 내려가면 다음 주기에 다시 시도한다 */ }
}

/* ── 초기화 ─────────────────────────────────────────────────────── */
async function boot() {
  try {
    state.repo = await api("/api/repo");
    const repoEl = $("#repo");
    repoEl.textContent = "";
    repoEl.appendChild(el("b", "", state.repo.name));
    repoEl.appendChild(document.createTextNode(" · " + (state.repo.branch || "")));
    document.title = "mgit — " + state.repo.name;

    const log = await api("/api/log");
    state.commits = log.commits || [];
    state.layout = log.graph || {nodes: [], edges: [], width: 0};
    state.byId = new Map(state.commits.map(c => [c.sha, c]));
    state.notes = await api("/api/notes?status=all") || [];
    state.noteCounts = log.noteCounts || {};

    const st = await api("/api/state");
    state.seq = st.seq;
    state.notesRev = st.notesRev;
    if (st.explicit) setView("detail");
    await select(st.target || (state.commits[0] && state.commits[0].sha));
    render();
  } catch (e) {
    document.body.innerHTML = `<div class="err" style="margin:24px">시작 실패: ${e.message}</div>`;
    return;
  }

  initTheme();
  initSplitter();

  $("#rows").addEventListener("click", e => {
    const row = e.target.closest(".crow"); if (!row) return;
    select(row.dataset.sha);
  });
  $("#rows").addEventListener("dblclick", e => {
    const row = e.target.closest(".crow"); if (!row) return;
    select(row.dataset.sha); setView("detail");
  });
  $("#rows").addEventListener("keydown", e => {
    const row = e.target.closest(".crow"); if (!row) return;
    if (e.key === "Enter" || e.key === " ") { e.preventDefault(); select(row.dataset.sha); setView("detail"); }
    if (e.key === "ArrowDown" && row.nextElementSibling) { e.preventDefault(); row.nextElementSibling.focus(); }
    if (e.key === "ArrowUp" && row.previousElementSibling) { e.preventDefault(); row.previousElementSibling.focus(); }
  });

  $("#v-graph").onclick = () => setView("graph");
  $("#v-detail").onclick = () => setView("detail");
  $("#godetail").onclick = () => setView("detail");
  $("#gograph").onclick = () => setView("graph");
  $("#filterbtn").onclick = () => { state.showAll = !state.showAll; drawNotes(); };
  $("#addcommit").onclick = () => {
    state.composer = "commit"; selectTab("tab-notes"); drawNotes();
    const ta = $("#composer-ta"); if (ta) ta.focus();
  };
  $("#copy").onclick = () => {
    const t = $("#cmd").textContent;
    if (navigator.clipboard) navigator.clipboard.writeText(t).then(() => toast("복사했습니다"));
  };
  document.querySelectorAll(".tab").forEach(t => { t.onclick = () => selectTab(t.id); });

  setInterval(pollState, 1500);
}

boot();

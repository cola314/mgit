"use strict";

/* mgit 뷰어 — 데이터는 전부 로컬 API 에서 온다. 레인 배치는 서버(internal/graph)가
   계산해서 내려주므로 여기서는 그리기만 한다. */

const R = 32, LW = 16, PAD = 16;   // 행 높이 / 레인 간격 / 좌측 여백
const $ = s => document.querySelector(s);

const state = {
  repo: null,
  commits: [],
  byId: new Map(),
  layout: {nodes: [], edges: [], width: 0},
  noteCounts: {},
  notes: [],
  sel: null,
  detail: null,
  diff: null,
  view: "graph",
  showAll: true,
  composer: null,      // {line, path} | "commit"
  flashNote: null,
  seq: -1,
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

function graphWidth() {
  return PAD * 2 + Math.max(0, state.layout.width - 1) * LW;
}

function drawGraph() {
  const svg = $("#lanes");
  const gw = graphWidth(), h = state.commits.length * R;
  svg.setAttribute("width", gw + 8);
  svg.setAttribute("height", h);
  svg.setAttribute("viewBox", `0 0 ${gw + 8} ${h}`);

  const laneOf = new Map(state.layout.nodes.map(n => [n.sha, n.lane]));
  let s = "";
  for (const e of state.layout.edges) {
    s += `<path d="${edgePath(e)}" fill="none" stroke="${laneVar(e.toLane)}" stroke-width="1.8"`
       + (e.dangling ? ` stroke-dasharray="3 3" opacity=".45"` : ` opacity=".9"`) + `/>`;
  }
  state.layout.nodes.forEach(n => {
    const c = state.byId.get(n.sha);
    const merge = c && c.parents && c.parents.length > 1;
    const x = laneX(n.lane), y = rowY(n.row);
    s += `<circle cx="${x}" cy="${y}" r="${merge ? 3.6 : 4.8}"`
       + ` fill="${merge ? "var(--panel)" : laneVar(n.lane)}"`
       + ` stroke="${laneVar(n.lane)}" stroke-width="${merge ? 2 : 1.5}"/>`;
    if (state.noteCounts[n.sha])
      s += `<circle cx="${x}" cy="${y}" r="8.5" fill="none" stroke="var(--accent-solid)" stroke-width="1.4" opacity=".9"/>`;
  });
  svg.innerHTML = s;
  void laneOf;
}

function drawRows() {
  const box = $("#rows");
  box.textContent = "";
  const gw = graphWidth();

  for (const c of state.commits) {
    const row = document.createElement("div");
    row.className = "crow" + (c.parents && c.parents.length > 1 ? " merge" : "")
                  + (c.sha === state.sel ? " sel" : "");
    row.tabIndex = 0;
    row.dataset.sha = c.sha;
    row.setAttribute("role", "button");
    row.setAttribute("aria-label", `${c.short} ${c.subject}`);

    const sp = document.createElement("div");
    sp.className = "gspace"; sp.style.width = (gw + 8) + "px";
    row.appendChild(sp);

    const nd = document.createElement("div");
    nd.className = "notedot";
    const cnt = state.noteCounts[c.sha] || 0;
    if (cnt) { const b = document.createElement("b"); b.textContent = cnt; b.title = `${cnt}개 메모`; nd.appendChild(b); }
    row.appendChild(nd);

    const subj = document.createElement("div");
    subj.className = "csubj";
    for (const ref of c.refs || []) subj.appendChild(refChip(ref));
    subj.appendChild(document.createTextNode(c.subject));
    row.appendChild(subj);

    const meta = document.createElement("div");
    meta.className = "cmeta";
    for (const [cls, val] of [["au", c.author], ["dt", fmtDate(c.date)], ["sh", c.short]]) {
      const e = document.createElement("span"); e.className = cls; e.textContent = val || ""; meta.appendChild(e);
    }
    row.appendChild(meta);
    box.appendChild(row);
  }
  $("#graphmeta").textContent = `${state.commits.length} commits · ${state.layout.width} lanes`;
}

function refChip(ref) {
  const chip = document.createElement("span");
  chip.className = "chip " + ref.kind;
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

/* ── 커밋 상세 + diff ───────────────────────────────────────────── */
function drawDetail() {
  const c = state.byId.get(state.sel);
  const box = $("#cdetail");
  box.textContent = "";
  if (!c) return;

  const subj = document.createElement("div");
  subj.className = "subj";
  for (const ref of c.refs || []) subj.appendChild(refChip(ref));
  subj.appendChild(document.createTextNode(c.subject));
  box.appendChild(subj);

  const l2 = document.createElement("div");
  l2.className = "line2";
  const parents = (c.parents || []).map(p => p.slice(0, 9)).join(" ") || "—";
  for (const [cls, v] of [["sha", c.short], ["au", c.author], ["dt", fmtDate(c.date)],
                          ["par", "parents: " + parents]]) {
    const e = document.createElement("span"); e.className = cls; e.textContent = v; l2.appendChild(e);
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

  if (state.diff === null) {
    body.appendChild(el("div", "emptyish", "불러오는 중…"));
    return;
  }
  if (state.diff.error) {
    body.appendChild(el("div", "err", state.diff.error));
    return;
  }
  const files = state.diff.files || [];
  if (!files.length) {
    body.appendChild(el("div", "emptyish", "변경된 파일이 없습니다."));
    return;
  }

  for (const f of files) {
    const fh = document.createElement("div");
    fh.className = "filehead";
    const fp = el("span", "fp", f.oldPath && f.oldPath !== f.path ? `${f.oldPath} → ${f.path}` : f.path);
    fp.title = f.path;
    const st = document.createElement("span");
    st.className = "st";
    st.appendChild(el("span", "a", `+${f.add}`));
    st.appendChild(document.createTextNode(" "));
    st.appendChild(el("span", "d", `−${f.del}`));
    fh.appendChild(fp); fh.appendChild(st);
    body.appendChild(fh);

    if (f.binary) { body.appendChild(el("div", "emptyish", "바이너리 파일")); continue; }

    for (const hk of f.hunks || []) {
      body.appendChild(el("div", "hunkhead", hk.header));
      for (const L of hk.lines || []) {
        const anchor = L.newNo || 0;
        const line = document.createElement("div");
        line.className = "dline" + (L.kind === "add" ? " add" : L.kind === "del" ? " del" : "");
        const mine = anchor ? notesForLine(f.path, anchor) : [];
        if (anchor) {
          line.dataset.line = anchor;
          line.dataset.path = f.path;
          if (mine.length) line.classList.add("noted");
        }

        const gut = document.createElement("div");
        gut.className = "gut";
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
        line.appendChild(el("div", "txt", L.text || " "));
        body.appendChild(line);

        for (const n of mine) body.appendChild(inlineNote(n));
        if (state.composer && state.composer !== "commit" &&
            state.composer.line === anchor && state.composer.path === f.path) {
          body.appendChild(composer("line"));
        }
      }
    }
  }
  state.flashNote = null;
}

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function baseOf(p) { return p ? p.split("/").pop() : ""; }

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
  x.onclick = () => removeNote(n);
  ih.appendChild(x);
  box.appendChild(ih);

  box.appendChild(el("div", "ibody", n.body));
  if (n.resolution) box.appendChild(el("div", "ires", "→ " + n.resolution));

  const act = el("div", "iact");
  const tg = el("button", "btn sm" + (n.status === "open" ? " pri" : ""),
                n.status === "open" ? "완료 처리" : "다시 열기");
  tg.type = "button";
  tg.onclick = () => toggleNote(n);
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
  const where = kind === "commit"
    ? `${c ? c.short : ""} · 커밋 전체`
    : `${c ? c.short : ""} · ${baseOf(state.composer.path)}:${state.composer.line}`;
  cf.appendChild(el("span", "at", where));
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
    try {
      await createNote(kind, v);
    } catch (e) {
      toast("메모 저장 실패: " + e.message);
      save.disabled = false;
    }
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
function visibleNotes() {
  return state.showAll ? state.notes : state.notes.filter(n => n.anchor.commit === state.sel);
}

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
  // 내가 방금 만든 변경을 폴링이 "외부 변경"으로 오해해 토스트를 띄우지 않도록 맞춰둔다.
  try { state.notesRev = (await api("/api/state")).notesRev; } catch (_) {}
  render();
  if ($("#tab-cli").getAttribute("aria-selected") === "true") drawCli();
}

function jumpTo(n) {
  if (n.anchor.commit !== state.sel) { select(n.anchor.commit); }
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
  render();
  updateCmd();
  $("#dscroll").scrollTop = 0;
  try {
    state.diff = await api(`/api/diff/${sha}`);
  } catch (e) {
    state.diff = {error: "diff 를 불러오지 못했습니다: " + e.message};
  }
  if (state.sel === sha) drawDiff();
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
  drawGraph(); drawRows(); drawDetail(); drawDiff(); drawNotes();
}

/* ── 폴링 ───────────────────────────────────────────────────────── */
// 두 가지를 따라간다.
//   1. 두 번째 `mgit . -c <sha>` 실행이 바꾼 target
//   2. 에이전트가 `mgit note done` 으로 고친 메모 파일
// 둘 다 이 화면 밖에서 일어나는 변화라 폴링 말고는 알 방법이 없다.
async function pollState() {
  try {
    const st = await api("/api/state");

    if (st.notesRev !== state.notesRev) {
      const first = state.notesRev === undefined;
      state.notesRev = st.notesRev;
      if (!first) {
        await reloadNotes();
        toast("메모가 갱신되었습니다");
      }
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
    $("#repo").innerHTML = "";
    $("#repo").appendChild(document.createTextNode(""));
    const b = document.createElement("b");
    b.textContent = state.repo.name;
    $("#repo").appendChild(b);
    $("#repo").appendChild(document.createTextNode(" · " + (state.repo.branch || "")));
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
    await select(st.target || (state.commits[0] && state.commits[0].sha));
    // -c 로 커밋을 콕 집어 열었으면 그래프가 아니라 그 커밋의 상세로 바로 들어간다.
    if (st.explicit) setView("detail");
    render();
  } catch (e) {
    document.body.innerHTML = `<div class="err" style="margin:24px">시작 실패: ${e.message}</div>`;
    return;
  }

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
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault(); select(row.dataset.sha); setView("detail");
    }
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

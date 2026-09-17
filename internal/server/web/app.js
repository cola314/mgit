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
  composer: null,    // {path, line, endLine} | "commit"   endLine 0 이면 단일 줄
  replyTo: null,     // 답글을 쓰고 있는 부모 메모 ID
  scopeHead: false,  // true 면 현재 브랜치(HEAD)의 조상만
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
  // 그래프 열 폭이 자동이면 레인 수가 바뀔 때마다 다시 계산한다.
  if (COLW && COLW.graph === null) applyCols();

  for (const c of state.commits) {
    const row = el("div", "crow" + (c.parents && c.parents.length > 1 ? " merge" : "")
                            + (c.sha === state.sel ? " sel" : ""));
    row.tabIndex = 0;
    row.dataset.sha = c.sha;
    row.setAttribute("role", "button");
    row.setAttribute("aria-label", `${c.short} ${c.subject}`);

    row.appendChild(el("div", "gspace"));

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

/* 메모가 덮는 마지막 줄. endLine 이 없는 옛 메모는 시작 줄과 같다. */
const lastLineOf = n => (n.anchor.endLine > n.anchor.line ? n.anchor.endLine : n.anchor.line);
const isRange = n => n.anchor.endLine > n.anchor.line;

/* 이 커밋·파일에 달린 루트 메모만. 답글은 부모 박스 안에서 함께 그린다. */
function rootNotesFor(path) {
  return state.notes.filter(n =>
    n.kind === "line" && !n.parent && n.anchor.commit === state.sel && n.anchor.path === path);
}

/* 박스를 놓을 자리 — 범위의 끝 줄 뒤다. 그래야 코드 덩어리가 중간에 안 잘린다. */
function notesEndingAt(path, line) {
  return rootNotesFor(path).filter(n => lastLineOf(n) === line);
}

/* 하이라이트용 — 이 줄을 덮는 메모가 있는지. */
function notesCovering(path, line) {
  return rootNotesFor(path).filter(n => line >= n.anchor.line && line <= lastLineOf(n));
}

/* 지금 열려 있는 컴포저가 이 줄을 덮는지 (범위 선택 미리보기). */
function composerCovers(path, line) {
  const c = state.composer;
  if (!c || c === "commit" || c.path !== path) return false;
  const lo = Math.min(c.line, c.endLine || c.line);
  const hi = Math.max(c.line, c.endLine || c.line);
  return line >= lo && line <= hi;
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
  if (f.forcedText) {
    // git 이 .gitattributes 때문에 바이너리로 취급했지만 내용은 텍스트라 펼쳤다.
    const c = el("span", "chip", "text");
    c.title = ".gitattributes 에서 binary 로 선언된 파일 — 내용이 텍스트라 diff 를 펼쳐 보여준다";
    head.appendChild(c);
  }
  const st = el("span", "st");
  st.appendChild(el("span", "a", `+${f.add}`));
  st.appendChild(document.createTextNode(" "));
  st.appendChild(el("span", "d", `−${f.del}`));
  head.appendChild(st);

  if (!f.binary) {
    const open = el("button", "iconbtn", "⤢");
    open.type = "button";
    open.title = "파일 전문 보기";
    open.setAttribute("aria-label", `${baseOf(f.path)} 전문 보기`);
    open.style.width = "24px";
    open.style.height = "22px";
    open.onclick = ev => { ev.stopPropagation(); openFileView(f); };  // 접기와 겹치지 않게
    head.appendChild(open);
  }

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
      const ending = anchor ? notesEndingAt(f.path, anchor) : [];
      if (anchor) {
        line.dataset.line = anchor;
        // 범위 전체에 띠를 두른다. 시작·끝에만 모서리를 줘서 덩어리로 보이게 한다.
        const covering = notesCovering(f.path, anchor);
        if (covering.length) {
          line.classList.add("noted");
          if (covering.some(n => n.anchor.line === anchor)) line.classList.add("noted-top");
          if (covering.some(n => lastLineOf(n) === anchor)) line.classList.add("noted-bot");
        }
        if (composerCovers(f.path, anchor)) line.classList.add("picking");
      }

      const gut = el("div", "gut");
      gut.appendChild(el("i", "", L.oldNo ? String(L.oldNo) : ""));
      gut.appendChild(el("i", "", L.newNo ? String(L.newNo) : ""));
      if (anchor) {
        const b = el("i", "addbtn", "✎");
        b.tabIndex = 0;
        b.setAttribute("role", "button");
        b.title = `${anchor}번 줄에 메모 달기 (Shift+클릭으로 범위 지정)`;
        b.setAttribute("aria-label", b.title);
        const open = ev => {
          ev.preventDefault(); ev.stopPropagation();
          const c = state.composer;
          // Shift+클릭: 이미 이 파일에 컴포저가 열려 있으면 거기까지 범위를 늘린다.
          if (ev.shiftKey && c && c !== "commit" && c.path === f.path) {
            const lo = Math.min(c.line, anchor), hi = Math.max(c.line, anchor);
            state.composer = {path: f.path, line: lo, endLine: hi > lo ? hi : 0};
          } else {
            state.composer = {path: f.path, line: anchor, endLine: 0};
          }
          state.flashNote = null;
          const keep = $("#composer-ta") ? $("#composer-ta").value : "";
          drawDiff();
          const ta = $("#composer-ta");
          if (ta) { ta.value = keep; ta.focus(); }   // 범위를 늘려도 쓰던 글은 살린다
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

      for (const n of ending) sec.appendChild(inlineNote(n));
      // 컴포저도 범위 끝 뒤에 놓는다. 범위를 늘리면 박스가 따라 내려온다.
      if (state.composer && state.composer !== "commit" && state.composer.path === f.path &&
          (state.composer.endLine || state.composer.line) === anchor) {
        sec.appendChild(composer("line"));
      }
    }
    fbody.appendChild(sec);
  }
  card.appendChild(fbody);
  return card;
}

/* 메모 위치 표기. 범위면 시작-끝. */
function locLabel(n) {
  const base = baseOf(n.anchor.path);
  return isRange(n) ? `${base}:${n.anchor.line}-${n.anchor.endLine}` : `${base}:${n.anchor.line}`;
}

/* 이 메모에 달린 답글 (생성 순). */
const repliesOf = id => state.notes
  .filter(n => n.parent === id)
  .sort((a, b) => (a.created || "").localeCompare(b.created || ""));

function inlineNote(n) {
  const box = el("div", "inote " + n.status);
  box.dataset.id = n.id;
  if (state.flashNote === n.id) box.classList.add("flash");

  const ih = el("div", "ih");
  ih.appendChild(el("span", "nid", n.id));
  ih.appendChild(el("span", "nst", n.status));
  const at = el("span", "iat", locLabel(n));
  at.title = isRange(n)
    ? `${n.anchor.path}:${n.anchor.line}-${n.anchor.endLine} (${n.anchor.endLine - n.anchor.line + 1}줄)`
    : `${n.anchor.path}:${n.anchor.line}`;
  ih.appendChild(at);
  const x = el("button", "nx", "✕");
  x.type = "button"; x.title = "메모 삭제 (답글도 함께 지워집니다)";
  x.setAttribute("aria-label", `${n.id} 삭제`);
  x.onclick = ev => { ev.stopPropagation(); removeNote(n); };
  ih.appendChild(x);
  box.appendChild(ih);

  box.appendChild(el("div", "ibody", n.body));
  if (n.resolution) box.appendChild(el("div", "ires", "→ " + n.resolution));

  // 답글 — 부모 박스 안에 이어 붙인다. 대화를 한 덩어리로 읽히게.
  for (const r of repliesOf(n.id)) {
    const rb = el("div", "ireply");
    const rh = el("div", "ih");
    rh.appendChild(el("span", "nid", r.id));
    rh.appendChild(el("span", "iat", "답글"));
    const rx = el("button", "nx", "✕");
    rx.type = "button"; rx.title = "답글 삭제";
    rx.setAttribute("aria-label", `${r.id} 삭제`);
    rx.onclick = ev => { ev.stopPropagation(); removeNote(r); };
    rh.appendChild(rx);
    rb.appendChild(rh);
    rb.appendChild(el("div", "ibody", r.body));
    box.appendChild(rb);
  }

  if (state.replyTo === n.id) {
    box.appendChild(replyComposer(n));
  }

  const act = el("div", "iact");
  const tg = el("button", "btn sm" + (n.status === "open" ? " pri" : ""),
                n.status === "open" ? "완료 처리" : "다시 열기");
  tg.type = "button";
  tg.onclick = ev => { ev.stopPropagation(); toggleNote(n); };
  act.appendChild(tg);

  const rp = el("button", "btn sm", state.replyTo === n.id ? "답글 취소" : "답글");
  rp.type = "button";
  rp.onclick = ev => {
    ev.stopPropagation();
    state.replyTo = state.replyTo === n.id ? null : n.id;
    render();
    const ta = $("#reply-ta"); if (ta) ta.focus();
  };
  act.appendChild(rp);
  box.appendChild(act);
  return box;
}

/* 답글 입력칸. 부모 박스 안에 뜬다. */
function replyComposer(parent) {
  const box = el("div", "composer reply");
  const ta = document.createElement("textarea");
  ta.id = "reply-ta";
  ta.placeholder = `${parent.id} 에 답글… (Ctrl+Enter 저장)`;
  box.appendChild(ta);

  const cf = el("div", "cf");
  cf.appendChild(el("span", "at", `→ ${parent.id}`));
  const cancel = el("button", "btn", "취소");
  cancel.type = "button";
  cancel.onclick = ev => { ev.stopPropagation(); state.replyTo = null; render(); };
  const save = el("button", "btn pri", "답글 저장");
  save.type = "button";
  const submit = async () => {
    const v = ta.value.trim();
    if (!v) { ta.focus(); return; }
    save.disabled = true;
    try { await createReply(parent.id, v); }
    catch (e) { toast("답글 저장 실패: " + e.message); save.disabled = false; }
  };
  save.onclick = ev => { ev.stopPropagation(); submit(); };
  ta.addEventListener("keydown", ev => {
    if ((ev.ctrlKey || ev.metaKey) && ev.key === "Enter") { ev.preventDefault(); submit(); }
    if (ev.key === "Escape") { ev.preventDefault(); state.replyTo = null; render(); }
  });
  cf.appendChild(cancel);
  cf.appendChild(save);
  box.appendChild(cf);
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
  const span = kind === "commit" ? "" :
    (state.composer.endLine > state.composer.line
      ? `${state.composer.line}-${state.composer.endLine} (${state.composer.endLine - state.composer.line + 1}줄)`
      : `${state.composer.line}`);
  cf.appendChild(el("span", "at", kind === "commit"
    ? `${c ? c.short : ""} · 커밋 전체`
    : `${c ? c.short : ""} · ${baseOf(state.composer.path)}:${span}`));
  const hint = el("span", "at",
    kind === "commit" ? "Ctrl+Enter 저장" : "Shift+클릭으로 범위 · Ctrl+Enter 저장");
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

/* ── 파일 전문 뷰어 ─────────────────────────────────────────────── */
// diff 는 헝크 주변만 보여준다. 앞뒤 맥락이 필요할 때 그 커밋 시점의 파일 전체를
// 겹쳐 띄운다. 워킹트리가 아니라 커밋 시점을 읽는 게 핵심 — 지금 파일은 이미
// 달라져 있을 수 있다.
let fvReturnFocus = null;
let fvRuns = [];   // 변경 구간 목록 [{start,end}] — 연속된 줄은 한 덩어리
let fvIdx = -1;

// 연속된 줄 번호를 구간으로 묶는다. 374·375·376 이 각각 멈춤 지점이 되면
// 버튼을 세 번 눌러야 다음 변경으로 넘어가 성가시다.
function groupRuns(sorted) {
  const runs = [];
  for (const n of sorted) {
    const last = runs[runs.length - 1];
    if (last && n === last.end + 1) last.end = n;
    else runs.push({start: n, end: n});
  }
  return runs;
}

async function openFileView(f) {
  const ov = $("#fileview"), body = $("#fvbody");
  fvReturnFocus = document.activeElement;

  const title = $("#fvtitle");
  title.textContent = "";
  const dir = dirOf(f.path);
  if (dir) title.appendChild(el("span", "dir", dir));
  title.appendChild(document.createTextNode(baseOf(f.path)));
  title.title = f.path;

  const c = state.byId.get(state.sel);
  $("#fvmeta").textContent = c ? c.short : "";
  body.textContent = "";
  body.appendChild(el("div", "emptyish", "불러오는 중…"));
  ov.hidden = false;
  $("#fvclose").focus();

  let data;
  try {
    data = await api(`/api/file/${state.sel}?path=${encodeURIComponent(f.path)}`);
  } catch (e) {
    body.textContent = "";
    body.appendChild(el("div", "err", "파일을 불러오지 못했습니다: " + e.message));
    return;
  }
  if (ov.hidden) return;   // 로딩 중에 닫혔다

  body.textContent = "";
  if (data.binary) {
    body.appendChild(el("div", "emptyish", "바이너리 파일입니다."));
    return;
  }

  // 이 커밋에서 추가·수정된 줄을 표시해 전문 안에서도 diff 위치를 잃지 않게 한다.
  const changed = new Set();
  for (const hk of f.hunks || []) {
    for (const L of hk.lines || []) if (L.kind === "add" && L.newNo) changed.add(L.newNo);
  }

  const lang = HL.langOf(f.path);
  let hlState = {block: false};
  const frag = document.createDocumentFragment();

  data.lines.forEach((text, i) => {
    const n = i + 1;
    const line = el("div", "fvline" + (changed.has(n) ? " changed" : ""));
    line.dataset.line = n;
    line.appendChild(el("i", "no", String(n)));
    const code = el("div", "code");
    if (lang) {
      const r = HL.highlight(text, lang, hlState);
      hlState = r.state;
      code.innerHTML = r.html || " ";
    } else {
      code.textContent = text || " ";
    }
    line.appendChild(code);
    frag.appendChild(line);
  });
  body.appendChild(frag);

  if (data.truncated) {
    body.appendChild(el("div", "emptyish",
      `파일이 너무 길어 앞부분 ${data.lines.length}줄만 표시했습니다.`));
  }

  fvRuns = groupRuns([...changed].sort((a, b) => a - b));
  fvIdx = -1;
  $("#fvjump").hidden = fvRuns.length === 0;
  if (fvRuns.length) fvNext();
}

// 변경 구간을 순서대로 돈다. 끝에 닿으면 처음으로 감는다.
function fvNext() {
  if (!fvRuns.length) return;
  fvIdx = (fvIdx + 1) % fvRuns.length;
  const run = fvRuns[fvIdx];

  const btn = $("#fvjump");
  btn.textContent = fvRuns.length > 1
    ? `변경 ${fvIdx + 1}/${fvRuns.length} ↓`
    : "변경 위치";
  btn.title = fvRuns.length > 1
    ? `다음 변경 구간으로 (${run.start}${run.end > run.start ? "–" + run.end : ""}번 줄)`
    : "이 커밋에서 바뀐 줄로";

  // 구간 전체를 잠깐 강조한다. 첫 줄만 깜빡이면 어디까지가 변경인지 안 보인다.
  for (let n = run.start; n <= run.end; n++) {
    const el = document.querySelector(`.fvline[data-line="${n}"]`);
    if (!el) continue;
    if (n === run.start) el.scrollIntoView({block: "center"});
    el.classList.remove("flash");
    void el.offsetWidth;   // 애니메이션 재시작
    el.classList.add("flash");
  }
}

function closeFileView() {
  const ov = $("#fileview");
  if (ov.hidden) return;
  ov.hidden = true;
  $("#fvbody").textContent = "";
  if (fvReturnFocus && fvReturnFocus.isConnected) fvReturnFocus.focus();
  fvReturnFocus = null;
}

/* ── 메모 레일 ──────────────────────────────────────────────────── */
/* 레일에는 루트만 세운다. 답글은 각 박스 안에 붙는다. */
const visibleNotes = () =>
  (state.showAll ? state.notes : state.notes.filter(n => n.anchor.commit === state.sel))
    .filter(n => !n.parent);

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
      ? `${short} · ${locLabel(n)}`
      : `${short} · 커밋 전체`));
    box.appendChild(el("div", "nbody", n.body));
    if (n.resolution) box.appendChild(el("div", "nres", "→ " + n.resolution));

    const kids = repliesOf(n.id);
    for (const r of kids) {
      const rb = el("div", "nreply");
      rb.appendChild(el("span", "nid", r.id));
      rb.appendChild(el("div", "nbody", r.body));
      box.appendChild(rb);
    }

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

  // 답글은 처리 단위가 아니므로 개수에서 뺀다.
  const roots = state.notes.filter(n => !n.parent);
  const open = roots.filter(n => n.status === "open").length;
  $("#notecnt").textContent = `open ${open} / total ${roots.length}`;
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
    : {kind: "line", commit: state.sel, path: state.composer.path,
       line: state.composer.line, endLine: state.composer.endLine || 0, body};
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

async function createReply(parentId, body) {
  await api("/api/notes", {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify({parent: parentId, body}),
  });
  state.replyTo = null;
  await reloadNotes();
  toast("답글을 저장했습니다");
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

/* ── 브랜치 범위 ────────────────────────────────────────────────── */
// SourceTree 의 "현재 브랜치만 보기". 체크하면 HEAD 의 조상만 남아 그래프가
// 한 줄로 정리된다. 레인이 14개씩 되는 저장소에서 특히 쓸모 있다.
const SCOPE_KEY = "mgit.scope.head";

async function loadLog() {
  const log = await api("/api/log" + (state.scopeHead ? "?scope=head" : ""));
  state.commits = log.commits || [];
  state.layout = log.graph || {nodes: [], edges: [], width: 0};
  state.byId = new Map(state.commits.map(c => [c.sha, c]));
  state.noteCounts = log.noteCounts || {};
}

async function setScopeHead(on) {
  state.scopeHead = on;
  $("#scopehead").checked = on;
  try { localStorage.setItem(SCOPE_KEY, on ? "1" : "0"); } catch (_) {}

  try {
    await loadLog();
  } catch (e) {
    toast("목록을 불러오지 못했습니다: " + e.message);
    return;
  }
  // 걸러진 목록에 지금 보던 커밋이 없으면 맨 위로 옮긴다.
  if (!state.byId.has(state.sel)) await select(state.commits[0] && state.commits[0].sha);
  else render();
  $("#gscroll").scrollTop = 0;
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
  syncURL();
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
  syncURL();
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

/* ── 레이아웃: 컬럼 너비 / 메모 레일 ───────────────────────────── */
// 사이드 패널처럼 좁은 화면에서는 메타 컬럼이 설명을 잡아먹는다. 폭을 직접
// 조절할 수 있어야 하고, 0 까지 줄이면 아예 숨겨진다.
// graph 는 값이 null 이면 "레인 수에 맞춰 자동". 사용자가 한 번 끌면 그 값으로 고정된다.
const COLS = {graph: null, au: 82, dt: 104, sh: 74};
// 손잡이가 어느 쪽 경계에 붙어 있는지 — 그래프만 오른쪽 경계다.
const COL_DIR = {graph: +1, au: -1, dt: -1, sh: -1};
// v2: 예전에는 좁은 화면에서 작성자·날짜를 0 으로 접어 저장했다. 사용자가 열을
// 잃어버리고 되돌릴 방법도 못 찾는 문제가 있어 키를 올려 옛 값을 버린다.
const COL_KEY = "mgit.cols.v2";
const RAIL_KEY = "mgit.rail.width";
const RAIL_OPEN_KEY = "mgit.rail.open";
const NARROW = 900;   // 이 폭 미만이면 기본값을 좁게 잡는다

let COLW = null;   // 현재 컬럼 폭. graph 가 null 이면 자동.

// persist 는 사용자가 직접 조절했을 때만 true 다. 화면 폭에서 자동으로 정한 값을
// 저장해 버리면, 창을 넓혀도 좁을 때 계산한 폭이 계속 따라붙는다.
function applyCols(persist) {
  const root = $("#app");
  for (const k of Object.keys(COLS)) {
    const v = k === "graph" && COLW[k] === null ? autoGraphWidth() : COLW[k];
    root.style.setProperty(`--w-${k}`, v + "px");
    // 접힌 열은 여백까지 0 이어야 완전히 사라진다.
    root.style.setProperty(`--p-${k}`, (v > 0 ? 12 : 0) + "px");
  }
  if (persist) {
    try { localStorage.setItem(COL_KEY, JSON.stringify(COLW)); } catch (_) {}
  }
}

// 레인 수로 정해지는 그래프 폭. 좁은 화면에서는 상한을 둔다 — 레인이 14개면
// 240px 이 되어 사이드 패널에서 설명이 남지 않는다.
function autoGraphWidth() {
  const natural = graphWidth() + 8;
  return window.innerWidth < NARROW ? Math.min(natural, 120) : natural;
}

function loadCols() {
  try {
    const saved = JSON.parse(localStorage.getItem(COL_KEY) || "null");
    if (saved && typeof saved === "object") {
      const out = {};
      for (const k of Object.keys(COLS)) {
        out[k] = saved[k] === null || saved[k] === undefined
          ? COLS[k]
          : clamp(Number(saved[k]) || 0, 0, 600);
      }
      return out;
    }
  } catch (_) {}
  // 저장된 값이 없을 때: 좁은 화면이면 좁게 잡되 숨기지는 않는다.
  // 열을 0 으로 접는 건 사용자가 직접 끌었을 때만 일어나야 한다.
  return window.innerWidth < NARROW
    ? {graph: null, au: 56, dt: 78, sh: 70}
    : {...COLS};
}

function initColumns() {
  COLW = loadCols();
  applyCols();

  let dragging = null, startX = 0, startW = 0;

  for (const grip of document.querySelectorAll("#chead .grip")) {
    const key = grip.parentElement.dataset.col;
    const cur = () => (key === "graph" && COLW[key] === null ? autoGraphWidth() : COLW[key]);
    const nudge = d => { COLW[key] = clamp(cur() + d, 0, 600); applyCols(true); };

    grip.addEventListener("mousedown", e => {
      dragging = key; startX = e.clientX; startW = cur();
      grip.classList.add("dragging");
      document.body.classList.add("resizing-x");
      e.preventDefault();
    });
    grip.addEventListener("keydown", e => {
      const step = (e.shiftKey ? 32 : 8) * COL_DIR[key];
      if (e.key === "ArrowLeft") { e.preventDefault(); nudge(-step); }
      if (e.key === "ArrowRight") { e.preventDefault(); nudge(step); }
    });
    // 손잡이 더블클릭 = 그 열만 기본값 복원.
    grip.addEventListener("dblclick", e => {
      e.preventDefault();
      e.stopPropagation();
      COLW[key] = COLS[key];
      applyCols(true);
    });
  }

  // 헤더 빈 곳 더블클릭 = 전체 복원. 열을 0 까지 접어 버리면 손잡이를 다시 잡기
  // 어려우므로 빠져나올 길이 하나는 있어야 한다.
  $("#chead").addEventListener("dblclick", () => {
    COLW = {...COLS};
    applyCols(true);
    toast("열 너비를 기본값으로 되돌렸습니다");
  });

  window.addEventListener("mousemove", e => {
    if (!dragging) return;
    COLW[dragging] = clamp(startW + (e.clientX - startX) * COL_DIR[dragging], 0, 600);
    applyCols();
  });
  window.addEventListener("mouseup", () => {
    if (!dragging) return;
    document.querySelectorAll("#chead .grip").forEach(g => g.classList.remove("dragging"));
    document.body.classList.remove("resizing-x");
    dragging = null;
    applyCols(true);
  });
  window.addEventListener("resize", () => { if (COLW.graph === null) applyCols(); });
}

function setRail(open) {
  $("#app").dataset.rail = open ? "on" : "off";
  const btn = $("#togglerail");
  btn.textContent = open ? "메모 ▶" : "◀ 메모";
  btn.setAttribute("aria-expanded", String(open));
  try { localStorage.setItem(RAIL_OPEN_KEY, open ? "1" : "0"); } catch (_) {}
}

function initRail() {
  const sp = $("#railsplit"), app = $("#app");
  const limit = w => clamp(w, 220, Math.max(260, window.innerWidth - 320));

  const saved = parseInt(localStorage.getItem(RAIL_KEY) || "", 10);
  const initial = isNaN(saved) ? (window.innerWidth < NARROW ? 300 : 396) : saved;
  app.style.setProperty("--rail-w", limit(initial) + "px");

  // 좁은 화면에서는 처음부터 접어 둔다. diff 를 볼 폭이 남지 않기 때문이다.
  const savedOpen = localStorage.getItem(RAIL_OPEN_KEY);
  setRail(savedOpen === null ? window.innerWidth >= NARROW : savedOpen !== "0");

  $("#togglerail").onclick = () => setRail(app.dataset.rail === "off");

  let dragging = false, startX = 0, startW = 0;
  const curW = () => parseInt(getComputedStyle(app).getPropertyValue("--rail-w"), 10) || 396;
  const persist = () => localStorage.setItem(RAIL_KEY, String(curW()));

  sp.addEventListener("mousedown", e => {
    dragging = true; startX = e.clientX; startW = curW();
    sp.classList.add("dragging");
    document.body.classList.add("resizing-x");
    e.preventDefault();
  });
  window.addEventListener("mousemove", e => {
    if (!dragging) return;
    app.style.setProperty("--rail-w", limit(startW - (e.clientX - startX)) + "px");
  });
  window.addEventListener("mouseup", () => {
    if (!dragging) return;
    dragging = false;
    sp.classList.remove("dragging");
    document.body.classList.remove("resizing-x");
    persist();
  });
  sp.addEventListener("keydown", e => {
    const step = e.shiftKey ? 48 : 14;
    if (e.key === "ArrowLeft") { e.preventDefault(); app.style.setProperty("--rail-w", limit(curW() + step) + "px"); persist(); }
    if (e.key === "ArrowRight") { e.preventDefault(); app.style.setProperty("--rail-w", limit(curW() - step) + "px"); persist(); }
  });
  window.addEventListener("resize", () => {
    app.style.setProperty("--rail-w", limit(curW()) + "px");
  });
}

/* ── URL 쿼리 파라미터 ──────────────────────────────────────────── */
// 주소만으로 화면을 재현할 수 있어야 한다. 사이드 패널에서는 셸을 거치지 않고
// URL 을 바로 띄우는 게 빠르므로 CLI 의 -c 와 같은 일을 ?c= 로도 할 수 있게 한다.
//   ?c=<리비전>   HEAD~3, 태그, 브랜치, 짧은 SHA 등 git 문법 그대로
//   ?view=graph|detail
function syncURL() {
  const c = state.byId.get(state.sel);
  const q = new URLSearchParams();
  if (c) q.set("c", c.short);
  // view 는 항상 적는다. 빼면 새로고침 때 "c 만 있으면 detail" 규칙에 걸려
  // 그래프 뷰가 상세 뷰로 튄다.
  q.set("view", state.view);
  history.replaceState(null, "", location.pathname + "?" + q);
}

// 진입 시점의 쿼리를 즉시 붙잡아 둔다. boot 중 setView 가 syncURL 을 호출해
// 주소를 덮어쓰기 때문에, 나중에 location.search 를 읽으면 이미 지워져 있다.
const INITIAL_QUERY = new URLSearchParams(location.search);

// 진입 쿼리를 해석만 한다. 화면 전환은 boot 이 마지막에 한 번만 한다 —
// 여러 곳에서 setView 를 부르면 호출 순서에 따라 결과가 달라진다.
async function resolveURL() {
  const q = INITIAL_QUERY;
  const rev = q.get("c");
  const view = q.get("view");
  let target = null;

  if (rev) {
    try {
      const d = await api(`/api/commit/${encodeURIComponent(rev)}`);
      target = d.sha;
    } catch (_) {
      toast(`?c=${rev} 를 찾지 못했습니다`);
    }
  }

  let wantView = null;
  if (view === "graph" || view === "detail") wantView = view;
  else if (rev) wantView = "detail";   // 커밋만 콕 집었으면 상세로
  return {target, wantView};
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

    // 커밋·amend·브랜치 전환으로 HEAD 가 바뀌면 목록을 다시 읽는다.
    if (st.head && st.head !== state.head) {
      const first = state.head === undefined;
      state.head = st.head;
      if (!first) {
        // 브랜치가 바뀌었을 수 있으니 상단 표시도 같이 갱신한다.
        try {
          state.repo = await api("/api/repo");
          drawRepoHeader();
        } catch (_) { /* 이름 갱신 실패는 치명적이지 않다 */ }
        await loadLog();
        if (!state.byId.has(state.sel)) await select(state.commits[0] && state.commits[0].sha);
        else render();
        toast("새 커밋을 반영했습니다");
      }
    }

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

// 저장소 이름과 현재 브랜치를 상단에 그린다.
//
// 브랜치 전환 때 다시 불러야 한다. 한 번만 그려두면 체크아웃 후에도 옛 브랜치명이
// 남아, 그래프는 새 브랜치인데 이름은 옛것이라 화면이 서로 모순돼 보인다.
function drawRepoHeader() {
  const repoEl = $("#repo");
  repoEl.textContent = "";
  repoEl.appendChild(el("b", "", state.repo.name));
  repoEl.appendChild(document.createTextNode(" · " + (state.repo.branch || "")));
  document.title = "mgit — " + state.repo.name;
}

/* ── 초기화 ─────────────────────────────────────────────────────── */
async function boot() {
  try {
    state.repo = await api("/api/repo");
    drawRepoHeader();

    state.scopeHead = localStorage.getItem(SCOPE_KEY) === "1";
    $("#scopehead").checked = state.scopeHead;
    await loadLog();
    state.notes = await api("/api/notes?status=all") || [];

    const st = await api("/api/state");
    state.seq = st.seq;
    state.notesRev = st.notesRev;
    state.head = st.head;

    // URL 쿼리가 서버가 준 target 보다 우선한다. 주소를 직접 띄운 쪽이
    // 더 최근 의도이기 때문이다.
    const url = await resolveURL();
    await select(url.target || st.target || (state.commits[0] && state.commits[0].sha));

    // 뷰 결정은 여기 한 곳뿐이다. URL 이 명시했으면 그대로, 아니면 서버가
    // -c 로 커밋을 콕 집어 띄운 경우 상세로 연다.
    if (url.wantView) setView(url.wantView);
    else if (st.explicit) setView("detail");

    render();
  } catch (e) {
    document.body.innerHTML = `<div class="err" style="margin:24px">시작 실패: ${e.message}</div>`;
    return;
  }

  initTheme();
  initSplitter();
  initColumns();
  initRail();

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

  $("#fvclose").onclick = closeFileView;
  $("#fvbackdrop").onclick = closeFileView;
  $("#fvjump").onclick = fvNext;
  document.addEventListener("keydown", e => {
    if ($("#fileview").hidden) return;
    if (e.key === "Escape") { e.preventDefault(); closeFileView(); }
    // n = 다음 변경 구간. 파일이 길 때 버튼까지 마우스를 옮기지 않아도 되도록.
    if ((e.key === "n" || e.key === "N") && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault();
      fvNext();
    }
  });

  $("#scopehead").onchange = e => setScopeHead(e.target.checked);
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

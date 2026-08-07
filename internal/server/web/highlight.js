"use strict";

/* 의존성 없는 최소 문법 강조기.
 *
 * highlight.js 같은 라이브러리를 넣지 않는 이유는 두 가지다. 바이너리에 100KB 넘는
 * 서드파티를 박고 싶지 않고, diff 는 어차피 줄 단위로 그려서 라이브러리의 전체 문서
 * 파싱 전제가 맞지 않는다. 여기서는 줄 단위로 훑되 블록 주석 상태만 파일 안에서
 * 이어 붙인다. 완벽한 파서가 아니라 "읽기 편할 만큼"이 목표다.
 */

const HL = (() => {
  const C_LIKE = {
    line: ["//"], block: ["/*", "*/"], quotes: ["\"", "'"], annotation: true,
  };

  const KW = {
    java: "abstract assert boolean break byte case catch char class const continue default do double else enum extends final finally float for goto if implements import instanceof int interface long native new package private protected public return short static strictfp super switch synchronized this throw throws transient try void volatile while var record sealed permits yield true false null",
    go: "break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var nil true false iota make new len cap append copy delete panic recover",
    javascript: "async await break case catch class const continue debugger default delete do else export extends finally for from function get if import in instanceof let new of return set static super switch this throw try typeof var void while with yield true false null undefined",
    python: "and as assert async await break class continue def del elif else except finally for from global if import in is lambda nonlocal not or pass raise return try while with yield True False None self",
    sql: "select from where insert update delete into values set join left right inner outer full on group by order having limit offset union all as and or not null is in exists between like distinct create table alter drop index view primary key foreign references default case when then else end asc desc count sum avg min max",
    kotlin: "as break by class continue do else false for fun if in interface is null object package return super this throw true try typealias val var when while private public protected internal override suspend data sealed companion init constructor",
    csharp: "abstract as base bool break byte case catch char class const continue decimal default delegate do double else enum event explicit extern false finally fixed float for foreach get goto if implicit in int interface internal is lock long namespace new null object operator out override params private protected public readonly ref return sbyte sealed set short sizeof static string struct switch this throw true try typeof uint ulong unchecked unsafe ushort using var virtual void volatile while async await",
    yaml: "true false null yes no on off",
    json: "true false null",
  };

  const LANGS = {
    java: {...C_LIKE, kw: set(KW.java)},
    kotlin: {...C_LIKE, kw: set(KW.kotlin)},
    csharp: {...C_LIKE, kw: set(KW.csharp)},
    go: {...C_LIKE, quotes: ["\"", "'", "`"], annotation: false, kw: set(KW.go)},
    javascript: {...C_LIKE, quotes: ["\"", "'", "`"], annotation: false, kw: set(KW.javascript)},
    typescript: {...C_LIKE, quotes: ["\"", "'", "`"], annotation: true, kw: set(KW.javascript + " type namespace declare readonly implements enum")},
    python: {line: ["#"], block: null, quotes: ["\"", "'"], annotation: true, kw: set(KW.python)},
    sql: {line: ["--"], block: ["/*", "*/"], quotes: ["'", "\""], annotation: false, kw: set(KW.sql), ci: true},
    yaml: {line: ["#"], block: null, quotes: ["\"", "'"], annotation: false, kw: set(KW.yaml)},
    json: {line: [], block: null, quotes: ["\""], annotation: false, kw: set(KW.json)},
    xml: {line: [], block: ["<!--", "-->"], quotes: ["\"", "'"], annotation: false, kw: set(""), xml: true},
    properties: {line: ["#", "!"], block: null, quotes: [], annotation: false, kw: set("")},
    shell: {line: ["#"], block: null, quotes: ["\"", "'"], annotation: false,
            kw: set("if then else elif fi for while do done case esac function return export local echo cd set unset source")},
  };

  const EXT = {
    java: "java", kt: "kotlin", kts: "kotlin", cs: "csharp", go: "go",
    js: "javascript", jsx: "javascript", mjs: "javascript", cjs: "javascript",
    ts: "typescript", tsx: "typescript",
    py: "python", sql: "sql",
    yml: "yaml", yaml: "yaml", json: "json",
    xml: "xml", html: "xml", htm: "xml", xsd: "xml", pom: "xml", svg: "xml",
    properties: "properties", conf: "properties", ini: "properties",
    sh: "shell", bash: "shell", zsh: "shell",
  };

  function set(s) {
    return new Set(s.split(/\s+/).filter(Boolean));
  }

  function langOf(path) {
    if (!path) return null;
    const base = path.split("/").pop();
    const i = base.lastIndexOf(".");
    if (i < 0) return null;
    return EXT[base.slice(i + 1).toLowerCase()] || null;
  }

  function esc(s) {
    return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  }

  function tag(cls, text) {
    return `<span class="tok-${cls}">${esc(text)}</span>`;
  }

  const isIdStart = c => /[A-Za-z_$]/.test(c);
  const isId = c => /[A-Za-z0-9_$]/.test(c);

  /* highlight 는 한 줄을 HTML 로 바꾸고 다음 줄에 넘길 상태를 돌려준다.
     state 는 { block: bool } — 블록 주석이 이어지는 중인지. */
  function highlight(text, lang, state) {
    const L = LANGS[lang];
    if (!L || text === "") return {html: esc(text), state};

    let out = "", i = 0, n = text.length;
    let inBlock = state && state.block;

    if (inBlock && L.block) {
      const end = text.indexOf(L.block[1]);
      if (end < 0) return {html: tag("com", text), state: {block: true}};
      out += tag("com", text.slice(0, end + L.block[1].length));
      i = end + L.block[1].length;
      inBlock = false;
    }

    while (i < n) {
      const rest = text.slice(i);

      // 한 줄 주석 — 나머지 전부
      let hitLine = false;
      for (const lc of L.line) {
        if (rest.startsWith(lc)) {
          out += tag("com", rest);
          i = n;
          hitLine = true;
          break;
        }
      }
      if (hitLine) break;

      // 블록 주석
      if (L.block && rest.startsWith(L.block[0])) {
        const end = text.indexOf(L.block[1], i + L.block[0].length);
        if (end < 0) {
          out += tag("com", rest);
          i = n;
          inBlock = true;
          break;
        }
        out += tag("com", text.slice(i, end + L.block[1].length));
        i = end + L.block[1].length;
        continue;
      }

      // 문자열 — 닫히지 않으면 줄 끝까지 (줄 단위 처리의 한계, 다음 줄로 넘기지 않는다)
      const q = L.quotes.find(ch => rest.startsWith(ch));
      if (q) {
        let j = i + q.length;
        while (j < n) {
          if (text[j] === "\\") { j += 2; continue; }
          if (text.startsWith(q, j)) { j += q.length; break; }
          j++;
        }
        out += tag("str", text.slice(i, Math.min(j, n)));
        i = Math.min(j, n);
        continue;
      }

      // 애노테이션/데코레이터
      if (L.annotation && text[i] === "@" && i + 1 < n && isIdStart(text[i + 1])) {
        let j = i + 1;
        while (j < n && isId(text[j])) j++;
        out += tag("anno", text.slice(i, j));
        i = j;
        continue;
      }

      // 숫자
      if (/[0-9]/.test(text[i]) && (i === 0 || !isId(text[i - 1]))) {
        let j = i;
        while (j < n && /[0-9a-fA-FxXoObB._]/.test(text[j])) j++;
        // 접미사(L, f, d 등)
        while (j < n && /[LlFfDdUu]/.test(text[j])) j++;
        out += tag("num", text.slice(i, j));
        i = j;
        continue;
      }

      // 식별자
      if (isIdStart(text[i])) {
        let j = i;
        while (j < n && isId(text[j])) j++;
        const word = text.slice(i, j);
        const key = L.ci ? word.toLowerCase() : word;

        let k = j;
        while (k < n && text[k] === " ") k++;
        const callish = text[k] === "(";

        if (L.kw.has(key)) out += tag("kw", word);
        else if (callish) out += tag("fn", word);
        else if (/^[A-Z][A-Za-z0-9_$]*$/.test(word)) out += tag("type", word);
        else out += esc(word);
        i = j;
        continue;
      }

      out += esc(text[i]);
      i++;
    }

    return {html: out, state: {block: inBlock}};
  }

  return {highlight, langOf};
})();

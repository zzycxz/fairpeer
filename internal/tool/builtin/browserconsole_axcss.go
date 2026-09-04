// browserconsole_axcss.go — CSS computation for AX-listed elements.
//
// The picker's AX rows historically carried only snapshot refs (e36): fine
// for an immediate panel click, poison for recorded skills (refs die with
// the session — "no snapshot taken"). This pass matches each AX row to its
// DOM element (by accessible name over the interactive-candidate pool) and
// computes the same selector ladder the DOM complement uses, so picking a
// row fills the target with `css;;text=name` — stable by construction.
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chromedp/chromedp"
)

// axRowCSSJS takes SELS (JSON [{ref,role,name}]) and returns [{ref,css}].
// Best effort: rows without a name or without a DOM match come back bare.
//
// Selector ladder: #id (stable only) → [name=] → [data-testid/ref] →
// [aria-label] → ExtJS stable prefix → unique class → ancestor-scoped →
// nth-of-type path → text= anchor (last resort). Dynamic IDs and CSS-in-JS
// hashed class names are filtered so selectors survive re-renders and
// rebuilds.
//
// Shadow DOM: the candidate pool scans shadow roots so Web Components are
// reachable.
const axRowCSSJS = `(function(){
  var rows = SELS;
  var out = [];
  // --- helpers ---
  function isDynId(id) {
    if (!id) return false;
    if (/^ext-gen\d+$/.test(id)) return true;
    if (/^widget-[a-z]+-\d+/i.test(id)) return true;
    if (/-(\d{3,})(?:-|$)/.test(id) && !/^[a-z]+-[a-z]/i.test(id)) return true;
    return false;
  }
  var HASH_CLASS_RE = /^(css|emotion|jss|styled|sc)-[a-z0-9]{4,}$/i;
  function nameOf(el) {
    // Visible label for matching/text= anchor. The name attribute is NOT
    // user-visible — it's used by selectorFor's CSS step, not by text=.
    return (el.getAttribute('aria-label') || el.getAttribute('alt') || el.getAttribute('placeholder')
      || el.getAttribute('title') || (el.innerText || '')).trim().replace(/\s+/g, ' ').slice(0, 80);
  }
  function unique(sel) {
    try { return document.querySelectorAll(sel).length === 1 ? sel : ''; } catch (e) { return ''; }
  }
  function selectorFor(el) {
    var tag = el.tagName.toLowerCase();
    var nm = el.getAttribute('name');
    // 1. Stable ID
    if (el.id && !isDynId(el.id)) return '#' + CSS.escape(el.id);
    // 2. name attribute — form elements: use directly (first-match is correct
    //    for semantic HTML names); refine with type/readonly if available.
    //    Escape quotes/backslashes — the final fallback returns this selector
    //    WITHOUT a unique() guard, so an unescaped value would be invalid CSS.
    if (nm) {
      nm = nm.replace(/\\/g, '\\\\').replace(/"/g, '\\"');
      var byNameType = tag + '[name="' + nm + '"]';
      var tp = el.getAttribute('type');
      if (tp) byNameType += '[type="' + tp + '"]';
      if (unique(byNameType)) return byNameType;
      if (el.hasAttribute('readonly')) {
        var byNameRo = tag + '[name="' + nm + '"][readonly]';
        if (unique(byNameRo)) return byNameRo;
      }
      var byName = tag + '[name="' + nm + '"]';
      if (unique(byName)) return byName;
      // name not unique — still use it (first querySelector match is
      // correct for semantic form field names on most pages)
      return byName;
    }
    // 3. data-testid / data-ref
    var dref = el.getAttribute('data-ref') || el.getAttribute('data-testid');
    if (dref) {
      var attr = el.hasAttribute('data-ref') ? 'data-ref' : 'data-testid';
      var byRef = '[' + attr + '="' + dref + '"]';
      if (unique(byRef)) return byRef;
    }
    // 4. aria-label
    var aria = el.getAttribute('aria-label');
    if (aria) {
      var byAria = '[aria-label="' + aria.replace(/\\/g, '\\\\').replace(/"/g, '\\"') + '"]';
      if (unique(byAria)) return byAria;
    }
    // 5. ExtJS stable prefix
    if (el.id) {
      var stable = el.id.replace(/-\d+(?:-\w+)?$/, '');
      if (stable && stable !== el.id) {
        var combo = '[id^="' + stable + '"]' + (el.getAttribute('type') ? '[type="' + el.getAttribute('type') + '"]' : '');
        if (unique(combo)) return combo;
      }
    }
    // 6. Unique class (skip hashed class names)
    var cls = (el.getAttribute('class') || '').split(/\s+/).filter(Boolean);
    for (var i = 0; i < cls.length; i++) {
      if (HASH_CLASS_RE.test(cls[i])) continue;
      var c = tag + '.' + CSS.escape(cls[i]);
      if (unique(c)) return c;
    }
    if (cls.length) {
      var filtered = cls.filter(function(c){ return !HASH_CLASS_RE.test(c); });
      if (filtered.length) {
        var full = tag + '.' + filtered.map(CSS.escape).join('.');
        if (unique(full)) return full;
      }
    }
    // 7. Ancestor-scoped descendant
    var scope = el.parentElement, hops = 0;
    while (scope && hops < 8 && scope.nodeType === 1 && scope.tagName !== 'BODY' && (!scope.id || isDynId(scope.id))) {
      scope = scope.parentElement; hops++;
    }
    if (scope && scope.id && scope.tagName !== 'BODY' && !isDynId(scope.id)) {
      var scoped = '#' + CSS.escape(scope.id) + ' ' + tag + (cls.length ? '.' + cls.filter(function(c){return !HASH_CLASS_RE.test(c)}).map(CSS.escape).join('.') : '');
      if (unique(scoped)) return scoped;
    }
    // 8. Structural nth-of-type path
    var parts = [], cur = el;
    for (var d = 0; d < 14 && cur && cur.nodeType === 1 && cur.tagName !== 'BODY'; d++) {
      if (cur.id && !isDynId(cur.id)) { parts.unshift('#' + CSS.escape(cur.id)); cur = null; break; }
      var part = cur.tagName.toLowerCase();
      var sib = cur.parentElement ? cur.parentElement.children : [];
      var idx = 1;
      for (var s = 0; s < sib.length; s++) { if (sib[s] === cur) break; if (sib[s].tagName === cur.tagName) idx++; }
      if (sib.length > 1) part += ':nth-of-type(' + idx + ')';
      parts.unshift(part);
      cur = cur.parentElement;
    }
    if (parts.length !== 0) {
      var path = parts.join(' > ');
      var hitPath = unique(path);
      if (hitPath) return path;
    }
    // 9. text= visible label — the LAST resort. It must not preempt the
    // scope/structural rungs: same-class elements with a label (the
    // Naive-UI twin-textarea case) still have a computable unique CSS, and
    // the row's css field is consumed as a raw selector by picks/tests —
    // a text= anchor there breaks querySelectorAll. Anchored execution
    // chains (css;;text=) resolve the label anyway when CSS truly fails.
    var label = nameOf(el);
    if (!label && (tag === 'input' || tag === 'textarea')) label = (el.value || '').trim().slice(0, 80);
    if (label) return 'text=' + label;
    return '';
  }
  // --- collect candidate pool from light DOM + shadow roots ---
  var pool = [];
  function pushVisible(n){
    var r0 = n.getBoundingClientRect();
    if (r0.width < 1 || r0.height < 1) return;
    var st = getComputedStyle(n);
    if (st.visibility === 'hidden' || st.display === 'none') return;
    pool.push(n);
  }
  function collectPool(root) {
    root.querySelectorAll('a[href],button,input,select,textarea,[contenteditable="true"],[role="button"],[role="link"],[role="tab"],[onclick]').forEach(pushVisible);
    root.querySelectorAll('div,span').forEach(function(n){
      if (getComputedStyle(n).cursor === 'pointer') pushVisible(n);
    });
    root.querySelectorAll('*').forEach(function(el){
      if (el.shadowRoot) collectPool(el.shadowRoot);
    });
  }
  collectPool(document);
  var labeled = [];
  for (var i = 0; i < pool.length; i++) {
    var n = pool[i];
    var l = nameOf(n);
    if (l) labeled.push({ el: n, label: l });
  }
  var used = [];
  var isUsed = function(el){ return used.indexOf(el) !== -1; };
  var out = [];
  for (var r = 0; r < rows.length; r++) {
    var row = rows[r];
    var hit = null;
    if (row.name) {
      var want = row.name;
      for (var j = 0; j < labeled.length; j++) {
        if (labeled[j].label === want && !isUsed(labeled[j].el)) { hit = labeled[j].el; break; }
      }
      if (!hit) {
        for (var k = 0; k < labeled.length; k++) {
          var lab = labeled[k].label;
          var fwd = lab.length < 60 && lab.indexOf(want) !== -1;
          var rev = lab.length >= 2 && want.indexOf(lab) !== -1;
          if ((fwd || rev) && !isUsed(labeled[k].el)) { hit = labeled[k].el; break; }
        }
      }
    }
    // Fallback: match by value for form fields with empty accessible name
    // (ExtJS/Dojo inputs often lack aria-label/placeholder — AX tree reports
    // name="" but value="2026-09-02..."; match against DOM input.value).
    if (!hit && row.value && row.role && (row.role === 'textbox' || row.role === 'searchbox' || row.role === 'combobox' || row.role === 'spinbutton')) {
      for (var m = 0; m < pool.length; m++) {
        var el = pool[m];
        var tg = el.tagName.toLowerCase();
        if (tg !== 'input' && tg !== 'textarea' && tg !== 'select') continue;
        if (isUsed(el)) continue;
        if ((el.value || '').trim() === row.value.trim()) { hit = el; break; }
      }
    }
    if (hit) used.push(hit);
    out.push({ ref: row.ref, css: hit ? selectorFor(hit) : '' });
  }
  return JSON.stringify(out);
})()`

// axRowCSS runs the matcher for one session's current page.
func axRowCSS(ctx context.Context, rows []axRow) (map[string]string, error) {
	payload, err := json.Marshal(rows)
	if err != nil {
		return nil, err
	}
	expr := strings.Replace(axRowCSSJS, "SELS", string(payload), 1)
	var raw string
	if err := chromedp.Run(ctx, chromedp.Evaluate(expr, &raw)); err != nil {
		return nil, fmt.Errorf("ax css pass: %w", err)
	}
	var found []struct {
		Ref string `json:"ref"`
		CSS string `json:"css"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &found); err != nil {
		return nil, fmt.Errorf("ax css parse: %w", err)
	}
	out := make(map[string]string, len(found))
	for _, f := range found {
		if f.CSS != "" {
			out[f.Ref] = f.CSS
		}
	}
	return out, nil
}

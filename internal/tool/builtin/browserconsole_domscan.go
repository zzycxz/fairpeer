// browserconsole_domscan.go — DOM-level clickable candidates for the element
// picker, complementing the AX-tree listing.
//
// Why: Vue/React component libraries (Naive-UI, MUI, Element-Plus) render
// "buttons" as <img>/<div>/<span> with listeners attached from JS. The
// accessibility tree gives them no interactive role (often no name at all),
// so the role-based listing is blind to them — a real send button was
// missing from the picker while the AX tree happily reported a page full of
// static text. The heuristic below is the browser-use approach: visible
// elements that are img/svg, carry an onclick, or have a button-ish class,
// plus the pointer-cursor signal restricted to img/svg/tabindex (pointer-only
// on generic divs is hover-effect noise).
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chromedp/chromedp"
)

// domCandidateJS collects AX-blind clickables with a stable selector each.
// The selector ladder: #id (stable only) → [name=] → [data-testid/ref] →
// [aria-label] → ExtJS stable prefix → unique class → nth-of-type path →
// text= anchor (last resort). Elements without any unique handle are
// skipped.
//
// Shadow DOM: recursively scans shadow roots so Web Components are reachable.
const domCandidateJS = `(function(){
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
  function visible(el) {
    var r = el.getBoundingClientRect();
    if (r.width < 4 || r.height < 4) return false;
    var st = getComputedStyle(el);
    return st.visibility !== 'hidden' && st.display !== 'none';
  }
  function nameOf(el) {
    // Visible label for matching/text= anchor. The name attribute is NOT
    // user-visible text — it's used by selectorFor's CSS step, never by
    // text= anchors (which resolve by what the user can SEE on the page).
    return (el.getAttribute('aria-label') || el.getAttribute('alt') || el.getAttribute('placeholder')
      || el.getAttribute('title') || (el.innerText || '')).trim().replace(/\s+/g, ' ').slice(0, 80);
  }
  function unique(sel) {
    try { return document.querySelectorAll(sel).length === 1; } catch (e) { return false; }
  }
  function selectorFor(el) {
    var tag = el.tagName.toLowerCase();
    // 1. Stable ID (skip ExtJS/Dojo/jQuery-UI dynamic IDs)
    if (el.id && !isDynId(el.id)) return '#' + CSS.escape(el.id);
    // 2. name attribute — form elements: use directly (first-match is correct
    //    for semantic HTML names); refine with type/readonly if available.
    //    Escape quotes/backslashes — the final fallback returns this selector
    //    WITHOUT a unique() guard, so an unescaped value would be invalid CSS.
    var nm = el.getAttribute('name');
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
    // 3. data-testid / data-ref (testing frameworks, component libraries)
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
    // 5. ExtJS stable prefix: strip numeric suffix, match by prefix + tag/type
    if (el.id) {
      var stable = el.id.replace(/-\d+(?:-\w+)?$/, '');
      if (stable && stable !== el.id) {
        var combo = '[id^="' + stable + '"]' + (el.getAttribute('type') ? '[type="' + el.getAttribute('type') + '"]' : '');
        if (unique(combo)) return combo;
      }
    }
    // 6. Unique class (skip CSS-in-JS hashed class names)
    var cls = (el.getAttribute('class') || '').split(/\s+/).filter(Boolean);
    for (var i = 0; i < cls.length; i++) {
      if (HASH_CLASS_RE.test(cls[i])) continue;
      var c = tag + '.' + CSS.escape(cls[i]);
      try { if (document.querySelectorAll(c).length === 1) return c; } catch (e) {}
    }
    // 7. Structural nth-of-type path (skip dynamic ancestor IDs)
    var parts = [], cur = el;
    for (var d = 0; d < 6 && cur && cur.nodeType === 1 && cur.tagName !== 'BODY'; d++) {
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
      try { if (document.querySelectorAll(path).length === 1) return path; } catch (e) {}
    }
    // 8. text= visible label — LAST resort (see axcss: the selector field is
    // consumed as raw CSS; scope/structural rungs must get their chance
    // first, text= only when no unique selector is computable at all).
    var label = nameOf(el);
    if (!label && (tag === 'input' || tag === 'textarea')) label = (el.value || '').trim().slice(0, 80);
    if (label) return 'text=' + label;
    return '';
  }
  // --- collect from light DOM + shadow roots ---
  function collect(root) {
    var nodes = root.querySelectorAll('img, svg, [onclick], [class*="btn" i], [class*="button" i], [tabindex]:not([tabindex="-1"]), [contenteditable="true"], input, select, textarea, div, span');
    for (var i = 0; i < nodes.length && out.length < 80; i++) {
      var el = nodes[i];
      var tag = el.tagName.toLowerCase();
      if ((tag === 'div' || tag === 'span') && !el.hasAttribute('onclick')
        && !/btn|button/i.test(el.getAttribute('class') || '')
        && el.getAttribute('contenteditable') !== 'true') {
        var r0 = el.getBoundingClientRect();
        if (r0.width < 4 || r0.height < 4) continue;
      }
      // Skip standard a/button (AX pass covers them) but KEEP input/select/textarea —
      // ExtJS/Dojo wrappers often hide them from the AX tree.
      // Also keep <a>/<button> that carry ExtJS indicator classes or onclick —
      // the AX tree may miss them when the accessible name computation fails.
      if ((tag === 'a' || tag === 'button')
        && !/btn|button/i.test(el.getAttribute('class') || '')
        && !el.hasAttribute('onclick') && !el.hasAttribute('tabindex')) continue;
      if (!visible(el)) continue;
      var hint = el.hasAttribute('onclick') || /btn|button/i.test(el.getAttribute('class') || '')
        || el.getAttribute('contenteditable') === 'true';
      var isForm = (tag === 'input' || tag === 'select' || tag === 'textarea');
      var pointer = getComputedStyle(el).cursor === 'pointer';
      if (!hint && !pointer && !isForm) continue;
      if (!hint && !isForm && pointer && tag !== 'img' && tag !== 'svg' && !el.hasAttribute('tabindex')
        && !nameOf(el)) continue;
      var sel = selectorFor(el);
      if (!sel) continue;
      out.push({ role: tag, name: nameOf(el) || sel, selector: sel });
    }
    // Recurse into shadow roots
    var all = root.querySelectorAll('*');
    for (var j = 0; j < all.length; j++) {
      if (all[j].shadowRoot) collect(all[j].shadowRoot);
    }
  }
  collect(document);
  return JSON.stringify(out);
})()`

// scanDomCandidates runs the heuristic on the console session's current page.
func scanDomCandidates(ctx context.Context) ([]ConsoleElement, error) {
	var raw string
	if err := chromedp.Run(ctx, chromedp.Evaluate(domCandidateJS, &raw)); err != nil {
		return nil, fmt.Errorf("dom candidates: %w", err)
	}
	var found []struct {
		Role     string `json:"role"`
		Name     string `json:"name"`
		Selector string `json:"selector"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &found); err != nil {
		return nil, fmt.Errorf("dom candidates parse: %w", err)
	}
	out := make([]ConsoleElement, 0, len(found))
	for _, f := range found {
		if f.Selector == "" {
			continue
		}
		out = append(out, ConsoleElement{Ref: f.Selector, Role: f.Role, Name: f.Name})
	}
	return out, nil
}

// browsermarkdown.go — browser_extract's format=markdown renderer.
//
// AI-answer blocks, docs panels and rich previews carry structure (headings,
// bold, code, lists) that plain-text extraction flattens. This compact
// HTML→Markdown walk preserves the shapes ops flows actually reuse; it is
// deliberately not a full Turndown — tables are deferred to format=table,
// unknown tags recurse transparently.
package builtin

import (
	"context"
	"fmt"
	"strings"

	"github.com/chromedp/chromedp"
)

// extractMarkdownJS renders SEL (empty = body) as Markdown.
// Shadow DOM: queries pierce shadow boundaries; child iteration includes
// shadow root content so Web Components render correctly.
const extractMarkdownJS = `(function(){
  function queryDeep(sel) {
    var el = document.querySelector(sel);
    if (el) return el;
    var found = null;
    document.querySelectorAll('*').forEach(function(n){
      if (!found && n.shadowRoot) {
        try { found = n.shadowRoot.querySelector(sel); } catch(e) {}
      }
    });
    return found;
  }
  var root = SEL ? queryDeep(SEL) : document.body;
  if (!root) return '(element not found)';
  var BT = String.fromCharCode(96);
  function childNodesDeep(node) {
    var nodes = Array.from(node.childNodes);
    if (node.shadowRoot) nodes = nodes.concat(Array.from(node.shadowRoot.childNodes));
    return nodes;
  }
  function inline(node){
    var out = '';
    var children = childNodesDeep(node);
    for (var i = 0; i < children.length; i++) {
      var n = children[i];
      if (n.nodeType === 3) { out += n.textContent.replace(/\s+/g, ' '); continue; }
      if (n.nodeType !== 1) continue;
      var tag = n.tagName.toLowerCase();
      var inner = inline(n);
      if (tag === 'strong' || tag === 'b') { out += inner.trim() ? '**' + inner + '**' : ''; }
      else if (tag === 'em' || tag === 'i') { out += inner.trim() ? '*' + inner + '*' : ''; }
      else if (tag === 'code') { out += BT + n.textContent + BT; }
      else if (tag === 'br') { out += '\n'; }
      else if (tag === 'a') {
        var href = n.getAttribute('href') || '';
        out += inner.trim() && href && (href.indexOf('http') === 0 || href.indexOf('/') === 0)
          ? '[' + inner.trim() + '](' + href + ')' : inner;
      }
      else if (tag === 'img') { var alt = n.getAttribute('alt') || ''; out += alt ? '![' + alt + ']' : ''; }
      else { out += inner; }
    }
    return out;
  }
  var BLOCKISH = 'p,div,h1,h2,h3,h4,h5,h6,ul,ol,pre,blockquote,table,section,article';
  function block(node, depth){
    var out = '';
    var children = childNodesDeep(node);
    for (var i = 0; i < children.length; i++) {
      var n = children[i];
      if (n.nodeType === 3) { var t = n.textContent.trim(); if (t) out += t + '\n\n'; continue; }
      if (n.nodeType !== 1) continue;
      var tag = n.tagName.toLowerCase();
      if (tag === 'h1' || tag === 'h2' || tag === 'h3' || tag === 'h4' || tag === 'h5' || tag === 'h6') {
        var hashes = ''; for (var h = 0; h < parseInt(tag.charAt(1), 10); h++) hashes += '#';
        out += hashes + ' ' + inline(n).trim() + '\n\n';
      } else if (tag === 'p') {
        var pt = inline(n).trim(); if (pt) out += pt + '\n\n';
      } else if (tag === 'div') {
        if (n.querySelector(BLOCKISH)) out += block(n, depth);
        else { var dt = inline(n).trim(); if (dt) out += dt + '\n\n'; }
      } else if (tag === 'pre') {
        out += BT+BT+BT + '\n' + n.textContent.replace(/\n$/, '') + '\n' + BT+BT+BT + '\n\n';
      } else if (tag === 'blockquote') {
        var q = block(n, depth).trim();
        if (q) out += q.split('\n').map(function(l){ return '> ' + l; }).join('\n') + '\n\n';
      } else if (tag === 'ul' || tag === 'ol') {
        var idx = 0;
        for (var j = 0; j < n.childNodes.length; j++) {
          var li = n.childNodes[j];
          if (li.nodeType !== 1 || li.tagName.toLowerCase() !== 'li') continue;
          idx++;
          var marker = tag === 'ol' ? (idx + '. ') : '- ';
          out += '  '.repeat(depth) + marker + inline(li).trim() + '\n';
          var subs = li.querySelectorAll('ul,ol');
          for (var k = 0; k < subs.length; k++) out += block(subs[k], depth + 1);
        }
        out += '\n';
      } else if (tag === 'hr') {
        out += '---\n\n';
      } else if (tag === 'table') {
        out += block(n, depth);
      } else {
        out += block(n, depth);
      }
    }
    return out;
  }
  var md = block(root, 0).replace(/\n{3,}/g, '\n\n').trim();
  return md || '(no content)';
})()`

// extractMarkdown runs the renderer on one session's current page.
func extractMarkdown(ctx context.Context, s *browserSession, sel string) (string, error) {
	expr := strings.Replace(extractMarkdownJS, "SEL", fmt.Sprintf("%q", sel), 2)
	var text string
	if err := runBrowserAction(ctx, s, chromedp.Evaluate(expr, &text)); err != nil {
		return "", fmt.Errorf("extract markdown %q: %w", sel, err)
	}
	return text, nil
}

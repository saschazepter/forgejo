import {svg} from '../svg.ts';

export function makeCodeCopyButton(): HTMLButtonElement {
  const button = document.createElement('button');
  button.classList.add('code-copy', 'ui', 'button');
  button.innerHTML = svg('octicon-copy');
  return button;
}

export function renderCodeCopy() {
  const els = document.querySelectorAll('.markup .code-block code');
  if (!els.length) return;

  for (const el of els) {
    if (!el.textContent) continue;
    if (el.parentElement.classList?.contains('trim-empty-lines')) {
      for (let i = el.childNodes.length; i > 0; --i) {
        const n = el.childNodes[i - 1];
        if (n.nodeType === Node.TEXT_NODE && !n.textContent.trim()) {
          n.remove();
        } else {
          n.textContent += '\n';
        }
      }
    }
    // Suggestion, Mermaid and math blocks are replaced by some rendered content
    // (see markup/suggestion.js, markup/mermaid.js and markup/math.js), therefore
    // we should not add a copy button and leave it to the respective markup files.
    if (/(?:^|\s)language-(?:suggestion|mermaid|math)(?:\s|$)/i.test(el.className)) continue;
    const parent = el.closest('pre');
    let btn = parent.nextElementSibling?.classList?.contains('code-copy') ? parent.nextElementSibling : null;
    if (!btn) {
      btn = makeCodeCopyButton();
      parent.after(btn);
    }
    // remove final trailing newline introduced during HTML rendering
    btn.setAttribute('data-clipboard-text', el.textContent.replace(/\r?\n$/, ''));
  }
}

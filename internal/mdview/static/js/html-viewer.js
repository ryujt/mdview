export function isHtmlPath(path) {
  return /\.html?$/i.test(path);
}

// Uses the original URL so the HTML's relative CSS, script and image paths keep working.
// Runs in a separate-origin sandbox to isolate the document's styles and scripts from the viewer.
export function renderHTML(url, container) {
  const frame = document.createElement('iframe');
  frame.className = 'html-viewer';
  frame.title = 'HTML preview';
  frame.setAttribute('sandbox', 'allow-scripts allow-forms');
  frame.src = url;
  container.replaceChildren(frame);
}

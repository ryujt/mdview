export function isHtmlPath(path) {
  return /\.html?$/i.test(path);
}

// 원본 URL을 사용해 HTML의 상대 CSS·스크립트·이미지 경로를 유지한다.
// 별도 출처의 sandbox에서 실행해 문서 스타일과 스크립트를 뷰어에서 격리한다.
export function renderHTML(url, container) {
  const frame = document.createElement('iframe');
  frame.className = 'html-viewer';
  frame.title = 'HTML 미리보기';
  frame.setAttribute('sandbox', 'allow-scripts allow-forms');
  frame.src = url;
  container.replaceChildren(frame);
}

// web-url.js — 툴바 입력값을 열 수 있는 URL로 정규화하는 순수 함수
//
// 정본 검증은 서버(ValidateWebURL)가 한다. 여기서는 사용자가 scheme을 생략했을 때
// https(localhost·127.0.0.1·[::1]·*.localhost는 http)를 붙여 주고, http/https 이외의 scheme은 서버로 보내지 않는다.

const HTTP_SCHEME = /^https?:\/\//i;
// `localhost:3000`, `example.com:8080/a` 같은 host:port는 scheme으로 보지 않는다.
const ANY_SCHEME = /^[a-z][a-z0-9+.-]*:(?!\d+(?:[/?#]|$))/i;

const LOCAL_HOST = /^(localhost|127\.0\.0\.1|\[::1\]|[^/:?#]+\.localhost)(?=[:/?#]|$)/i;

/**
 * @param {string} input 사용자 입력
 * @returns {string} 열 URL. 열 수 없는 입력이면 빈 문자열.
 */
export function normalizeWebURL(input) {
  const value = String(input || '').trim();
  if (!value) return '';
  if (HTTP_SCHEME.test(value)) return value;
  if (ANY_SCHEME.test(value)) return '';
  const host = value.replace(/^\/\//, '');
  return `${LOCAL_HOST.test(host) ? 'http' : 'https'}://${host}`;
}

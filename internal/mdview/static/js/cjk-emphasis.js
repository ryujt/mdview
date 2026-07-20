// CJK(한글 등) 인접 강조 보정.
//
// CommonMark 의 emphasis flanking 규칙은 한글/CJK 글자를 일반 문자로 취급한다.
// 그래서 닫는 구분자(`**`/`*`/`__`/`_`) 앞이 문장부호이고 바로 뒤가 CJK 글자인
// 경우(예: `**(전이)**를`, `**기계(Machine)**는`) 닫는 구분자로 인정되지 않아
// `**` 가 그대로 노출된다. 반대로 여는 구분자 앞이 CJK, 뒤가 문장부호인 경우도
// 같은 이유로 실패한다.
//
// 해당 경계(부호↔구분자↔CJK)에 보이지 않는 ZWSP(U+200B)를 끼워 넣으면, 구분자에
// 인접한 글자가 "문장부호가 아님" 으로 바뀌어 표준 파서(marked/goldmark)가 강조를
// 정상 인식한다. ZWSP 는 화면/문서에서 보이지 않으므로 결과물에는 영향이 없다.
const ZWSP = '​';
// 문장부호 클래스: ASCII 구두점에서 강조 구분자(`*` `_`)와 코드스팬(`` ` ``)은 제외.
const P = "!-)+-/:-@\\[-^{-~¡¿‘’“”„–—…、。〈-】！）．：；？｛-･";
const CJK = "가-힣ㄱ-ㅎㅏ-ㅣ぀-ヿ㐀-鿿豈-﫿";
const CLOSE = new RegExp(`([${P}])(\\*\\*|\\*|__|_)(?=[${CJK}])`, 'g');
const OPEN = new RegExp(`([${CJK}])(\\*\\*|\\*|__|_)(?=[${P}])`, 'g');

export function fixCjkEmphasis(text) {
  if (!text || (text.indexOf('*') === -1 && text.indexOf('_') === -1)) return text;
  // 인라인 코드 스팬(`...`)은 보정 대상에서 제외한다.
  return text
    .split(/(`+[^`]*`+)/)
    .map((seg, i) => (i % 2 === 1
      ? seg
      : seg.replace(CLOSE, (m, a, d) => a + ZWSP + d).replace(OPEN, (m, a, d) => a + d + ZWSP)))
    .join('');
}

// Emphasis fix-up next to CJK (Hangul, etc.) characters.
//
// CommonMark's emphasis flanking rules treat Hangul/CJK characters as ordinary characters.
// So when a closing delimiter (`**`/`*`/`__`/`_`) is preceded by punctuation and immediately
// followed by a CJK character (e.g. `**(전이)**를`, `**기계(Machine)**는`), it is not recognized
// as a closing delimiter and the `**` shows up literally. Likewise, an opening delimiter preceded
// by CJK and followed by punctuation fails for the same reason.
//
// Inserting an invisible ZWSP (U+200B) at that boundary (punctuation <-> delimiter <-> CJK) makes
// the character adjacent to the delimiter "not punctuation", so standard parsers (marked/goldmark)
// recognize the emphasis correctly. ZWSP is invisible on screen and in documents, so output is unaffected.
const ZWSP = '​';
// Punctuation class: ASCII punctuation, excluding emphasis delimiters (`*` `_`) and code spans (`` ` ``).
const P = "!-)+-/:-@\\[-^{-~¡¿‘’“”„–—…、。〈-】！）．：；？｛-･";
const CJK = "가-힣ㄱ-ㅎㅏ-ㅣ぀-ヿ㐀-鿿豈-﫿";
const CLOSE = new RegExp(`([${P}])(\\*\\*|\\*|__|_)(?=[${CJK}])`, 'g');
const OPEN = new RegExp(`([${CJK}])(\\*\\*|\\*|__|_)(?=[${P}])`, 'g');

export function fixCjkEmphasis(text) {
  if (!text || (text.indexOf('*') === -1 && text.indexOf('_') === -1)) return text;
  // Inline code spans (`...`) are excluded from the fix-up.
  return text
    .split(/(`+[^`]*`+)/)
    .map((seg, i) => (i % 2 === 1
      ? seg
      : seg.replace(CLOSE, (m, a, d) => a + ZWSP + d).replace(OPEN, (m, a, d) => a + d + ZWSP)))
    .join('');
}

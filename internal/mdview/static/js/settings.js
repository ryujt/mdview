const SETTINGS_API = '/api/settings';

export function initSettings() {
  const button = document.getElementById('settings-button');
  const dialog = document.getElementById('settings-dialog');
  const form = document.getElementById('settings-form');
  const fields = document.getElementById('settings-fields');
  const key = document.getElementById('settings-api-key');
  const keyHelp = document.getElementById('settings-key-help');
  const keyBadge = document.getElementById('settings-key-badge');
  const clear = document.getElementById('settings-key-clear');
  const visibility = document.getElementById('settings-key-visibility');
  const model = document.getElementById('settings-model');
  const modelHelp = document.getElementById('settings-model-help');
  const message = document.getElementById('settings-message');
  const save = document.getElementById('settings-save');
  let settings = null;
  let clearKey = false;
  let busy = false;
  let generation = 0;

  function showMessage(text, error = false) {
    message.textContent = text;
    message.classList.toggle('settings-error', error);
  }

  function resetKey() {
    key.value = '';
    key.type = 'password';
    visibility.textContent = '표시';
    visibility.setAttribute('aria-pressed', 'false');
    visibility.setAttribute('aria-label', '입력한 API 키 표시');
    clearKey = false;
  }

  function modelDescription() {
    const option = settings?.models.find(option => option.id === model.value);
    modelHelp.textContent = option?.timestamps
      ? '현재 기본 모델입니다. 발화별 시간 정보를 문서 조작 기록과 함께 저장합니다.'
      : '발화별 시간 정보 없이 전체 전사문을 저장합니다. 문서 조작 시간은 그대로 기록됩니다.';
  }

  function applySettings(data) {
    settings = data;
    resetKey();
    keyBadge.textContent = data.has_api_key ? '설정됨' : '미설정';
    keyBadge.classList.toggle('configured', data.has_api_key);
    key.placeholder = data.has_saved_api_key ? '새 키를 입력하면 기존 키를 변경합니다' : 'sk-…';
    keyHelp.textContent = data.key_source === 'environment'
      ? 'OPENAI_API_KEY 환경 변수의 키가 우선 적용됩니다. 여기에서 저장한 키는 환경 변수가 없을 때 사용합니다.'
      : data.has_saved_api_key
        ? '저장된 키는 표시하지 않습니다. 비워 두면 기존 키를 유지합니다.'
        : '키 없이도 문서 열람과 녹음은 사용할 수 있습니다.';
    clear.disabled = !data.has_saved_api_key;
    clear.textContent = '저장된 키 삭제';
    model.replaceChildren(...data.models.map(option => {
      const element = document.createElement('option');
      element.value = option.id;
      element.textContent = option.label;
      return element;
    }));
    model.value = data.transcribe_model;
    modelDescription();
  }

  async function responseJSON(response) {
    if (!response.ok) {
      // Responses never include credentials. Do not log request bodies or key input.
      throw new Error((await response.text()).trim() || '설정 요청에 실패했습니다.');
    }
    return response.json();
  }

  button.addEventListener('click', async () => {
    if (dialog.open) return;
    const current = ++generation;
    resetKey();
    fields.disabled = true;
    save.disabled = true;
    showMessage('설정을 불러오는 중…');
    dialog.showModal();
    try {
      const data = await responseJSON(await fetch(SETTINGS_API, { cache: 'no-store' }));
      if (current !== generation || !dialog.open) return;
      applySettings(data);
      fields.disabled = false;
      save.disabled = false;
      showMessage('');
      (data.has_api_key ? model : key).focus();
    } catch {
      if (current === generation && dialog.open) showMessage('설정을 불러오지 못했습니다. 창을 닫고 다시 열어 주세요.', true);
    }
  });

  model.addEventListener('change', modelDescription);
  visibility.addEventListener('click', () => {
    const visible = key.type === 'password';
    key.type = visible ? 'text' : 'password';
    visibility.textContent = visible ? '숨기기' : '표시';
    visibility.setAttribute('aria-pressed', String(visible));
    visibility.setAttribute('aria-label', visible ? '입력한 API 키 숨기기' : '입력한 API 키 표시');
  });
  clear.addEventListener('click', () => {
    key.value = '';
    clearKey = true;
    clear.disabled = true;
    clear.textContent = '삭제 예정';
    showMessage('저장을 누르면 이 컴퓨터에 저장된 키가 삭제됩니다.');
  });
  key.addEventListener('input', () => {
    if (key.value) {
      clearKey = false;
      clear.disabled = !settings?.has_saved_api_key;
      clear.textContent = '저장된 키 삭제';
    }
    showMessage('');
  });
  form.addEventListener('submit', async event => {
    event.preventDefault();
    if (busy || fields.disabled) return;
    const current = generation;
    const payload = { transcribe_model: model.value };
    if (key.value.trim()) payload.openai_api_key = key.value.trim();
    else if (clearKey) payload.openai_api_key = '';
    busy = true;
    button.disabled = true;
    fields.disabled = true;
    save.disabled = true;
    save.textContent = '저장 중…';
    showMessage('');
    try {
      const data = await responseJSON(await fetch(SETTINGS_API, {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload),
      }));
      if (current === generation && dialog.open) {
        applySettings(data);
        showMessage('저장했습니다. 다음 녹음의 음성 전사부터 적용됩니다.');
      }
    } catch (error) {
      if (current === generation && dialog.open) showMessage(error.message, true);
    } finally {
      busy = false;
      button.disabled = false;
      if (current === generation && dialog.open) {
        fields.disabled = false;
        save.disabled = false;
        save.textContent = '저장';
      }
    }
  });
  document.getElementById('settings-close').addEventListener('click', () => dialog.close());
  document.getElementById('settings-cancel').addEventListener('click', () => dialog.close());
  dialog.addEventListener('close', () => {
    generation += 1;
    resetKey();
    save.textContent = '저장';
    button.focus();
  });
}

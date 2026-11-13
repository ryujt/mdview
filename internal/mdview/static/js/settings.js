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
    visibility.textContent = 'Show';
    visibility.setAttribute('aria-pressed', 'false');
    visibility.setAttribute('aria-label', 'Show entered API key');
    clearKey = false;
  }

  function modelDescription() {
    const option = settings?.models.find(option => option.id === model.value);
    modelHelp.textContent = option?.timestamps
      ? 'The current default model. Saves per-utterance timing along with the document interaction log.'
      : 'Saves the full transcript without per-utterance timing. Document interaction times are still recorded.';
  }

  function applySettings(data) {
    settings = data;
    resetKey();
    keyBadge.textContent = data.has_api_key ? 'Set' : 'Not set';
    keyBadge.classList.toggle('configured', data.has_api_key);
    key.placeholder = data.has_saved_api_key ? 'Enter a new key to replace the existing one' : 'sk-…';
    keyHelp.textContent = data.key_source === 'environment'
      ? 'The key in the OPENAI_API_KEY environment variable takes precedence. The key saved here is used when the variable is not set.'
      : data.has_saved_api_key
        ? 'Saved keys are not displayed. Leave blank to keep the existing key.'
        : 'You can view documents and record without a key.';
    clear.disabled = !data.has_saved_api_key;
    clear.textContent = 'Delete saved key';
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
      throw new Error((await response.text()).trim() || 'Settings request failed.');
    }
    return response.json();
  }

  button.addEventListener('click', async () => {
    if (dialog.open) return;
    const current = ++generation;
    resetKey();
    fields.disabled = true;
    save.disabled = true;
    showMessage('Loading settings…');
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
      if (current === generation && dialog.open) showMessage('Failed to load settings. Close this window and open it again.', true);
    }
  });

  model.addEventListener('change', modelDescription);
  visibility.addEventListener('click', () => {
    const visible = key.type === 'password';
    key.type = visible ? 'text' : 'password';
    visibility.textContent = visible ? 'Hide' : 'Show';
    visibility.setAttribute('aria-pressed', String(visible));
    visibility.setAttribute('aria-label', visible ? 'Hide entered API key' : 'Show entered API key');
  });
  clear.addEventListener('click', () => {
    key.value = '';
    clearKey = true;
    clear.disabled = true;
    clear.textContent = 'Pending deletion';
    showMessage('Click Save to delete the key stored on this computer.');
  });
  key.addEventListener('input', () => {
    if (key.value) {
      clearKey = false;
      clear.disabled = !settings?.has_saved_api_key;
      clear.textContent = 'Delete saved key';
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
    save.textContent = 'Saving…';
    showMessage('');
    try {
      const data = await responseJSON(await fetch(SETTINGS_API, {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload),
      }));
      if (current === generation && dialog.open) {
        applySettings(data);
        showMessage('Saved. Takes effect starting with the next recording\'s transcription.');
      }
    } catch (error) {
      if (current === generation && dialog.open) showMessage(error.message, true);
    } finally {
      busy = false;
      button.disabled = false;
      if (current === generation && dialog.open) {
        fields.disabled = false;
        save.disabled = false;
        save.textContent = 'Save';
      }
    }
  });
  document.getElementById('settings-close').addEventListener('click', () => dialog.close());
  document.getElementById('settings-cancel').addEventListener('click', () => dialog.close());
  dialog.addEventListener('close', () => {
    generation += 1;
    resetKey();
    save.textContent = 'Save';
    button.focus();
  });
}

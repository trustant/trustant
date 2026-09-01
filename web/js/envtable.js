/*
 * Copyright 2025-2026 Nuvolaris Inc
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published
 * by the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <https://www.gnu.org/licenses/>.
 */

// Shared editable environment-variable table.
//
// app.html (modal) and appconfig.html (full page) both edit the same
// apps.<name>.development / .production config through POST /api/appconfig/<name>.
// They used to carry independent copies of the render/edit/save logic, which is
// how the modal ended up read-only while the page was editable. This module is
// the single implementation; each host supplies only its own element ids.
//
// Rows flagged `readonly` (the fixed OPS_* keys) keep a static Development value
// because the server regenerates it on every launch — editing it would be lost.
(function (global) {
    'use strict';

    function escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text === undefined || text === null ? '' : text;
        return div.innerHTML;
    }

    function stripInlineComment(value) {
        let quote = '';
        for (let i = 0; i < value.length; i++) {
            const ch = value[i];
            if (quote) {
                if (ch === quote && value[i - 1] !== '\\') quote = '';
                continue;
            }
            if (ch === '"' || ch === "'") {
                quote = ch;
                continue;
            }
            if (ch === '#' && (i === 0 || /\s/.test(value[i - 1]))) {
                return value.slice(0, i).trimEnd();
            }
        }
        return value.trimEnd();
    }

    function unquoteEnvValue(value) {
        const trimmed = value.trim();
        if (trimmed.length >= 2 && trimmed[0] === '"' && trimmed[trimmed.length - 1] === '"') {
            return trimmed.slice(1, -1)
                .replace(/\\n/g, '\n')
                .replace(/\\r/g, '\r')
                .replace(/\\t/g, '\t')
                .replace(/\\"/g, '"')
                .replace(/\\\\/g, '\\');
        }
        if (trimmed.length >= 2 && trimmed[0] === "'" && trimmed[trimmed.length - 1] === "'") {
            return trimmed.slice(1, -1).replace(/\\'/g, "'");
        }
        return stripInlineComment(trimmed).trim();
    }

    function parseEnvText(text) {
        const parsed = [];
        const seen = new Set();
        text.split(/\r?\n/).forEach((rawLine) => {
            let line = rawLine.trim();
            if (!line || line.startsWith('#')) return;
            if (line.startsWith('export ')) line = line.slice(7).trimStart();
            const eq = line.indexOf('=');
            if (eq <= 0) return;
            const name = line.slice(0, eq).trim();
            if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)) return;
            const value = unquoteEnvValue(line.slice(eq + 1));
            if (seen.has(name)) {
                const existing = parsed.find((item) => item.name === name);
                if (existing) existing.value = value;
            } else {
                parsed.push({ name, value });
                seen.add(name);
            }
        });
        return parsed;
    }

    // A variable is missing when it is declared but has no development value.
    // Fixed rows never count: the server supplies those itself on every launch.
    function isMissing(v) {
        return !v.readonly && !(v.dev_value || '').trim();
    }

    function EnvTable(options) {
        this.appName = options.appName;
        this.tbody = document.getElementById(options.tbodyId);
        this.columns = options.columns || 4;
        this.onChange = options.onChange || function () {};
        // Only flag empty rows once the caller knows the app actually declares
        // them as required, so an ordinary blank row is not painted as an error.
        this.highlightMissing = Boolean(options.highlightMissing);
        // A table-level read-only view: every cell renders as text and every
        // mutator is inert. Used by the workbench Env modal, which shows what a
        // launched app received — editing lives in the app list's Env action.
        this.readOnly = Boolean(options.readOnly);
        this.vars = [];
        this.savedSnapshot = '[]';
        this.localEnvKeys = [];
    }

    EnvTable.prototype.load = async function () {
        const resp = await fetch(`/api/appconfig/${encodeURIComponent(this.appName)}`);
        if (!resp.ok) {
            throw new Error(await resp.text());
        }
        const data = await resp.json();
        this.vars = data.vars || [];
        this.localEnvKeys = data.localenv_keys || [];
        this.savedSnapshot = JSON.stringify(this.vars);
        this.render();
        return data;
    };

    EnvTable.prototype.missingNames = function () {
        return this.vars.filter(isMissing).map((v) => v.name).filter(Boolean);
    };

    EnvTable.prototype.setHighlightMissing = function (on) {
        this.highlightMissing = Boolean(on);
        this.render();
    };

    EnvTable.prototype.render = function () {
        if (!this.tbody) return;
        if (this.vars.length === 0) {
            this.tbody.innerHTML = `<tr><td colspan="${this.columns}" class="text-center py-4 text-[color:var(--nu-muted)]">No variables</td></tr>`;
            this.onChange(this);
            return;
        }

        const id = this.instanceId();
        this.tbody.innerHTML = this.vars.map((v, i) => {
            const flag = this.highlightMissing && isMissing(v);
            const devClass = flag ? 'nu-input w-full px-2 py-1 text-xs border-red-500' : 'nu-input w-full px-2 py-1 text-xs';

            const nameField = (this.readOnly || v.readonly || v.fixed)
                ? `<span class="nu-code text-xs">${escapeHtml(v.name)}</span>`
                : `<input type="text" value="${escapeHtml(v.name)}" onchange="${id}.update(${i}, 'name', this.value)"
                    class="nu-input nu-code w-full px-2 py-1 text-xs">`;

            const devField = (this.readOnly || v.readonly)
                ? `<span class="text-xs text-[color:var(--nu-muted)]">${escapeHtml(v.dev_value)}</span>`
                : `<input type="text" value="${escapeHtml(v.dev_value)}" onchange="${id}.update(${i}, 'dev_value', this.value)"
                    class="${devClass}">`;

            const prodField = this.readOnly
                ? `<span class="text-xs text-[color:var(--nu-muted)]">${escapeHtml(v.prod_value)}</span>`
                : `<input type="text" value="${escapeHtml(v.prod_value)}" onchange="${id}.update(${i}, 'prod_value', this.value)"
                    class="nu-input w-full px-2 py-1 text-xs">`;

            const removeBtn = (this.readOnly || v.readonly || v.fixed)
                ? ''
                : `<button onclick="${id}.remove(${i})" class="nu-btn nu-btn-danger nu-btn-compact">Remove</button>`;

            return `
                <tr>
                    <td>${nameField}</td>
                    <td>${devField}</td>
                    <td>${prodField}</td>
                    <td class="text-right">${removeBtn}</td>
                </tr>`;
        }).join('');
        this.onChange(this);
    };

    // Inline handlers need a global path back to this instance; hosts register
    // themselves under a stable name via EnvTable.register.
    EnvTable.prototype.instanceId = function () {
        return this._globalName || 'envTable';
    };

    EnvTable.prototype.update = function (index, field, value) {
        this.vars[index][field] = value;
        // Re-render only when the flag state could change, so typing in a value
        // does not steal focus from the input being edited.
        if (this.highlightMissing && field === 'dev_value') {
            const stillMissing = isMissing(this.vars[index]);
            const input = this.tbody.querySelectorAll('tr')[index]?.querySelectorAll('input')[field === 'name' ? 0 : 1];
            if (input) input.classList.toggle('border-red-500', stillMissing);
        }
        this.onChange(this);
    };

    EnvTable.prototype.remove = function (index) {
        this.vars.splice(index, 1);
        this.render();
    };

    EnvTable.prototype.add = function () {
        if (this.readOnly) return;
        this.vars.push({ name: '', dev_value: '', prod_value: '', readonly: false, fixed: false });
        this.render();
        const inputs = this.tbody.querySelectorAll('input[type="text"]');
        if (inputs.length > 0) {
            inputs[inputs.length - 3]?.focus();
        }
    };

    EnvTable.prototype.importEnvText = function (text, target) {
        if (this.readOnly) return null;
        const entries = parseEnvText(text);
        if (entries.length === 0) return null;

        const scope = target === 'development' ? 'development' : 'production';
        let added = 0;
        let updated = 0;
        let skippedReadonly = 0;
        entries.forEach(({ name, value }) => {
            const existing = this.vars.find((item) => item.name === name);
            if (existing) {
                if (scope === 'development') {
                    if (existing.readonly) {
                        skippedReadonly++;
                        return;
                    }
                    existing.dev_value = value;
                } else {
                    existing.prod_value = value;
                }
                updated++;
            } else {
                this.vars.push({
                    name,
                    dev_value: scope === 'development' ? value : '',
                    prod_value: scope === 'production' ? value : '',
                    readonly: false,
                    fixed: false
                });
                added++;
            }
        });
        this.render();
        return { total: entries.length, added, updated, skippedReadonly, scope };
    };

    // Fills Development values from the workspace's predefined variables
    // (spec/2a-config.md). Unlike importEnvText this is deliberately
    // conservative, because it runs on a whole set of values the user did not
    // pick for this app:
    //
    // - only variables the app already declares are touched, so an app's .env
    //   stays what its template declared;
    // - only empty Development values are filled, so a value the user typed or
    //   the template supplied is never replaced and the button is safe to click
    //   twice;
    // - readonly rows (the fixed OPS_* keys) are skipped, matching
    //   importEnvText and isMissing.
    //
    // Nothing is saved here: the caller re-renders and the user still presses
    // Save, which is the only path by which a predefined value reaches an app.
    EnvTable.prototype.applyPredefined = function (predefined) {
        if (this.readOnly || !predefined) return { filled: 0, names: [] };
        const names = [];
        this.vars.forEach((v) => {
            if (v.readonly) return;
            if ((v.dev_value || '').trim() !== '') return;
            if (!Object.prototype.hasOwnProperty.call(predefined, v.name)) return;
            const value = predefined[v.name];
            if (!value) return;
            v.dev_value = value;
            names.push(v.name);
        });
        if (names.length > 0) {
            this.render();
            this.onChange();
        }
        return { filled: names.length, names: names };
    };

    // Adds variables chosen from the Shared Variables pool. Unlike
    // applyPredefined, which only fills rows the app already declares, this is
    // the user picking specific names, so it ALSO adds a row for a name the app
    // does not yet declare — that is the whole point of the button.
    //
    // The import is complete: a picked name is added WITH its value, so the row
    // holds exactly the name and value of the shared variable it came from —
    // app-produced entries included. A row left empty here would read as
    // unset until the next launch resolved it, which is what made an import look
    // like it had dropped half the variable.
    //
    // Never overwrites a non-empty value and never touches a readonly row,
    // matching applyPredefined. Nothing is saved here.
    EnvTable.prototype.addFromShared = function (entries, target) {
        if (this.readOnly || !Array.isArray(entries) || entries.length === 0) {
            return { added: 0, filled: 0, names: [] };
        }
        const scope = target === 'production' ? 'production' : 'development';
        const names = [];
        let added = 0;
        let filled = 0;

        entries.forEach((entry) => {
            const name = (entry && entry.name || '').trim();
            if (!name) return;
            // Every entry brings its value with it, whoever produced it.
            const value = (entry && entry.value) || '';
            const existing = this.vars.find((item) => item.name === name);
            if (existing) {
                if (scope === 'development' && existing.readonly) return;
                const field = scope === 'production' ? 'prod_value' : 'dev_value';
                if ((existing[field] || '').trim() !== '') return;
                if (value === '') return;
                existing[field] = value;
                filled++;
                names.push(name);
                return;
            }
            this.vars.push({
                name,
                dev_value: scope === 'development' ? value : '',
                prod_value: scope === 'production' ? value : '',
                readonly: false,
                fixed: false
            });
            added++;
            names.push(name);
        });

        if (names.length > 0) {
            this.render();
            this.onChange();
        }
        return { added, filled, names };
    };

    // Posts the whole vars array. Empty values are preserved server-side, which
    // is what keeps an unfilled required variable visible after a partial save.
    EnvTable.prototype.save = async function () {
        // A read-only table has nothing to save, and posting its rows would
        // rewrite the app's config from a view that was never editable.
        if (this.readOnly) return this.missingNames();
        const resp = await fetch(`/api/appconfig/${encodeURIComponent(this.appName)}`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ vars: this.vars })
        });
        if (!resp.ok) {
            throw new Error(await resp.text());
        }
        this.savedSnapshot = JSON.stringify(this.vars);
        return this.missingNames();
    };

    EnvTable.prototype.hasUnsavedChanges = function () {
        return JSON.stringify(this.vars) !== this.savedSnapshot;
    };

    // Binds the instance to a global name so the table's inline onchange/onclick
    // attributes can reach it. Host pages must NOT also declare `const <name>`:
    // a lexical binding and this window property are two separate slots, and the
    // inline attributes resolve through the scope chain to the window one.
    EnvTable.register = function (name, instance) {
        instance._globalName = name;
        global[name] = instance;
        return instance;
    };

    global.EnvTable = EnvTable;
    global.envTableEscapeHtml = escapeHtml;
    global.envTableParseEnvText = parseEnvText;
})(window);

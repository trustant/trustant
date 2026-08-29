// Shared service secrets — the two pickers (spec/18-shared.md).
//
// SharedPicker (producer side, app list): browse an app's ~/.ops/config.json and
// choose which leaves it publishes. Writes .env.shared.
//
// SharedVarsPicker (consumer side, env editor): pick names out of the workspace
// Shared Variables pool and add them to an app's variables.
//
// Both mount their own markup rather than each host page carrying a copy: the
// Env modal and the Env page drifted apart exactly that way before envtable.js
// unified them.
(function (global) {
    'use strict';

    function escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text == null ? '' : String(text);
        return div.innerHTML;
    }

    // A leaf is anything that is not an object: only leaves carry a value, and
    // an object node is a group of secrets, not one.
    function isLeaf(value) {
        return value === null || typeof value !== 'object';
    }

    // Values are masked by default so the picker is usable over a shared screen.
    // The user is choosing by path, not by value.
    function maskValue(value) {
        const text = value == null ? '' : String(value);
        if (text.length <= 4) return '••••';
        return '••••' + text.slice(-4);
    }

    // The name the file will carry once resolved: APPNAME__NAME.
    function varPrefix(appName) {
        return String(appName || '').toUpperCase() + '__';
    }

    function hasVarPrefix(name, appName) {
        const prefix = varPrefix(appName);
        return String(name || '').startsWith(prefix) && String(name).length > prefix.length;
    }

    // Default name for a newly checked leaf: the prefix, then the path
    // uppercased. The prefix is not doubled — ops ide login writes a block named
    // after the app, so appsuite + appsuite.secret.postgres gives
    // APPSUITE__SECRET_POSTGRES, not APPSUITE__APPSUITE_SECRET_POSTGRES.
    function defaultVarName(path, appName) {
        const prefix = varPrefix(appName);
        let segments = String(path).split('.');
        if (segments.length > 1 && segments[0].toUpperCase() === String(appName).toUpperCase()) {
            segments = segments.slice(1);
        }
        const tail = segments.join('_').toUpperCase().replace(/[^A-Z0-9_]/g, '_');
        return prefix + tail;
    }

    // --- SharedPicker: the producer side -------------------------------------

    function SharedPicker(options) {
        options = options || {};
        this.prefix = options.prefix || 'sharedPicker';
        this.appName = options.appName || '';
        this.tree = {};
        this.expanded = {};
        // path -> chosen variable name
        this.selection = {};
        this.revealed = {};
    }

    SharedPicker.prototype.el = function (suffix) {
        return document.getElementById(this.prefix + suffix);
    };

    SharedPicker.prototype.open = async function (appName) {
        if (appName) this.appName = appName;
        this.selection = {};
        this.expanded = {};
        this.revealed = {};

        const modal = this.el('Modal');
        if (!modal) return;
        modal.classList.remove('hidden');
        modal.classList.add('flex');

        this.el('Title').textContent = `Share service secrets — ${this.appName}`;
        this.el('Prefix').textContent = varPrefix(this.appName);
        this.el('Error').classList.add('hidden');
        this.el('Tree').innerHTML = '<div class="text-xs text-[color:var(--nu-muted)] py-4">Connecting to OpenServerless…</div>';
        this.renderSelection();

        try {
            // Pre-load what the app already shares, so saving one new variable
            // does not silently drop the rest: the save writes the whole file.
            const [treeResp, currentResp] = await Promise.all([
                fetch(`/api/shared/tree/${encodeURIComponent(this.appName)}`),
                fetch(`/api/shared/${encodeURIComponent(this.appName)}`)
            ]);
            if (!treeResp.ok) throw new Error(await treeResp.text());
            const treeData = await treeResp.json();
            this.tree = treeData.tree || {};

            if (currentResp.ok) {
                const current = await currentResp.json();
                (current.vars || []).forEach((v) => {
                    this.selection[v.path] = v.name;
                    // Expand the branches that already have a selection, so the
                    // user sees what is shared without hunting for it.
                    const segments = v.path.split('.');
                    for (let i = 1; i <= segments.length; i++) {
                        this.expanded[segments.slice(0, i).join('.')] = true;
                    }
                });
            }
            this.renderTree();
            this.renderSelection();
        } catch (err) {
            this.el('Tree').innerHTML = '';
            this.showError('Failed to read the service configuration: ' + err.message);
        }
    };

    SharedPicker.prototype.close = function () {
        const modal = this.el('Modal');
        if (!modal) return;
        modal.classList.add('hidden');
        modal.classList.remove('flex');
    };

    SharedPicker.prototype.showError = function (message) {
        const box = this.el('Error');
        box.textContent = message;
        box.classList.remove('hidden');
    };

    SharedPicker.prototype.toggleNode = function (path) {
        this.expanded[path] = !this.expanded[path];
        this.renderTree();
    };

    SharedPicker.prototype.toggleReveal = function (path) {
        this.revealed[path] = !this.revealed[path];
        this.renderTree();
    };

    SharedPicker.prototype.toggleLeaf = function (path) {
        if (Object.prototype.hasOwnProperty.call(this.selection, path)) {
            delete this.selection[path];
        } else {
            this.selection[path] = defaultVarName(path, this.appName);
        }
        this.renderTree();
        this.renderSelection();
    };

    SharedPicker.prototype.renameVar = function (path, name) {
        this.selection[path] = name;
        this.renderSelection();
    };

    SharedPicker.prototype.renderTree = function () {
        const container = this.el('Tree');
        if (!container) return;
        const id = this.globalName();
        const self = this;

        function renderLevel(node, parentPath, depth) {
            const keys = Object.keys(node).sort();
            return keys.map((key) => {
                const path = parentPath ? parentPath + '.' + key : key;
                const value = node[key];
                const pad = `padding-left:${depth * 12}px`;

                if (!isLeaf(value)) {
                    const open = Boolean(self.expanded[path]);
                    const children = open ? renderLevel(value, path, depth + 1) : '';
                    return `
                        <div>
                            <button type="button" onclick="${id}.toggleNode('${escapeHtml(path)}')"
                                class="w-full text-left text-xs py-1 hover:opacity-80" style="${pad}">
                                <span class="inline-block w-3">${open ? '▾' : '▸'}</span>
                                <span class="nu-code">${escapeHtml(key)}</span>
                            </button>
                            ${children}
                        </div>`;
                }

                const checked = Object.prototype.hasOwnProperty.call(self.selection, path);
                const shown = self.revealed[path] ? escapeHtml(value) : maskValue(value);
                return `
                    <div class="flex items-center gap-2 py-1 text-xs" style="${pad}">
                        <input type="checkbox" ${checked ? 'checked' : ''}
                            onchange="${id}.toggleLeaf('${escapeHtml(path)}')">
                        <span class="nu-code">${escapeHtml(key)}</span>
                        <span class="text-[color:var(--nu-muted)] truncate flex-1">${shown}</span>
                        <button type="button" onclick="${id}.toggleReveal('${escapeHtml(path)}')"
                            class="nu-btn nu-btn-secondary nu-btn-compact">${self.revealed[path] ? 'Hide' : 'Show'}</button>
                    </div>`;
            }).join('');
        }

        const body = renderLevel(this.tree, '', 0);
        container.innerHTML = body || '<div class="text-xs text-[color:var(--nu-muted)] py-4">No services are bound to this application.</div>';
    };

    SharedPicker.prototype.renderSelection = function () {
        const container = this.el('SelectedBody');
        if (!container) return;
        const id = this.globalName();
        const paths = Object.keys(this.selection).sort();

        if (paths.length === 0) {
            container.innerHTML = '<div class="text-xs text-[color:var(--nu-muted)] py-4">Nothing selected. Check a value on the left to share it.</div>';
            return;
        }

        const prefix = varPrefix(this.appName);
        container.innerHTML = paths.map((path) => {
            const name = this.selection[path];
            const valid = hasVarPrefix(name, this.appName);
            const inputClass = valid
                ? 'nu-input nu-code w-full px-2 py-1 text-xs'
                : 'nu-input nu-code w-full px-2 py-1 text-xs border-red-500';
            const note = valid
                ? ''
                : `<div class="text-xs text-red-500 mt-1">must start with ${escapeHtml(prefix)}</div>`;
            return `
                <div class="py-2 border-b border-[color:var(--nu-border)] last:border-0">
                    <div class="nu-code text-xs text-[color:var(--nu-muted)] truncate">${escapeHtml(path)}</div>
                    <input type="text" value="${escapeHtml(name)}" class="${inputClass} mt-1"
                        onchange="${id}.renameVar('${escapeHtml(path)}', this.value)">
                    ${note}
                </div>`;
        }).join('');
    };

    SharedPicker.prototype.save = async function () {
        const paths = Object.keys(this.selection);
        // Refused here first so the error is immediate; the server refusal is
        // the real gate, and it is total — one bad name writes nothing.
        const bad = paths.filter((p) => !hasVarPrefix(this.selection[p], this.appName));
        if (bad.length > 0) {
            this.showError(
                `Every shared variable must start with ${varPrefix(this.appName)} — fix: ` +
                bad.map((p) => this.selection[p] || '(empty)').join(', ')
            );
            return;
        }
        this.el('Error').classList.add('hidden');

        const vars = paths.map((p) => ({ name: this.selection[p], path: p }));
        try {
            const resp = await fetch(`/api/shared/${encodeURIComponent(this.appName)}`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ vars })
            });
            if (!resp.ok) throw new Error(await resp.text());
            const data = await resp.json();
            this.close();
            if (typeof this.onSaved === 'function') this.onSaved(data);
        } catch (err) {
            this.showError('Failed to save: ' + err.message);
        }
    };

    SharedPicker.prototype.globalName = function () {
        return this._globalName || 'sharedPicker';
    };

    // Injects the modal markup into whichever page loads this module.
    SharedPicker.mount = function (globalName, options) {
        const picker = new SharedPicker(options);
        picker._globalName = globalName;
        global[globalName] = picker;

        const prefix = picker.prefix;
        const container = document.createElement('div');
        container.innerHTML = `
            <div id="${prefix}Modal" class="hidden fixed inset-0 nu-modal-backdrop items-center justify-center z-50 p-4">
              <div class="nu-modal w-full max-w-5xl max-h-[90vh] flex flex-col">
                <div class="flex items-center justify-between px-5 py-4 border-b border-[color:var(--nu-border)]">
                  <h3 id="${prefix}Title" class="text-lg font-semibold">Share service secrets</h3>
                  <button onclick="${globalName}.close()" class="nu-btn nu-btn-secondary nu-btn-compact">Close</button>
                </div>
                <div class="px-5 py-3 text-xs text-[color:var(--nu-muted)] border-b border-[color:var(--nu-border)]">
                  <p>Choose which of this application's service secrets other applications may use.
                     Each one is stored as <span class="nu-code" id="${prefix}Prefix">APP__</span><span class="nu-code">NAME</span>.</p>
                  <p class="mt-1">Only names and paths are written to <span class="nu-code">.env.shared</span> and committed —
                     never the values. They are resolved again on every launch.</p>
                </div>
                <div id="${prefix}Error" class="hidden mx-5 mt-3 px-3 py-2 text-xs rounded bg-red-500/10 text-red-500"></div>
                <div class="flex-1 overflow-hidden grid md:grid-cols-2 gap-4 p-5">
                  <div class="flex flex-col overflow-hidden">
                    <div class="text-xs font-semibold mb-2">Service configuration</div>
                    <div id="${prefix}Tree" class="flex-1 overflow-y-auto nu-card p-2"></div>
                  </div>
                  <div class="flex flex-col overflow-hidden">
                    <div class="text-xs font-semibold mb-2">Shared variables</div>
                    <div id="${prefix}SelectedBody" class="flex-1 overflow-y-auto nu-card p-2"></div>
                  </div>
                </div>
                <div class="flex gap-2 px-5 py-4 border-t border-[color:var(--nu-border)]">
                  <button onclick="${globalName}.save()" class="nu-btn nu-btn-primary flex-1">Save</button>
                  <button onclick="${globalName}.close()" class="nu-btn nu-btn-secondary flex-1">Cancel</button>
                </div>
              </div>
            </div>`;
        document.body.appendChild(container.firstElementChild);
        return picker;
    };

    // --- SharedVarsPicker: the consumer side ---------------------------------

    function SharedVarsPicker(options) {
        options = options || {};
        this.prefix = options.prefix || 'sharedVarsPicker';
        this.table = options.table || null;
        this.entries = [];
        this.selected = {};
        this.target = 'development';
        this.host = '';
    }

    SharedVarsPicker.prototype.el = function (suffix) {
        return document.getElementById(this.prefix + suffix);
    };

    SharedVarsPicker.prototype.globalName = function () {
        return this._globalName || 'sharedVarsPicker';
    };

    SharedVarsPicker.prototype.open = async function () {
        const modal = this.el('Modal');
        if (!modal) return;
        this.selected = {};
        modal.classList.remove('hidden');
        modal.classList.add('flex');
        this.el('Body').innerHTML = '<div class="text-xs text-[color:var(--nu-muted)] py-4">Loading…</div>';

        try {
            const resp = await fetch('/api/predefined-env');
            if (!resp.ok) throw new Error(await resp.text());
            const data = await resp.json();
            this.devEntries = data.vars || [];
            this.production = data.production || {};
            this.render();
        } catch (err) {
            this.el('Body').innerHTML = `<div class="text-xs text-red-500 py-4">${escapeHtml('Failed to load: ' + err.message)}</div>`;
        }
    };

    SharedVarsPicker.prototype.close = function () {
        const modal = this.el('Modal');
        if (!modal) return;
        modal.classList.add('hidden');
        modal.classList.remove('flex');
    };

    SharedVarsPicker.prototype.setTarget = function (target) {
        this.target = target === 'production' ? 'production' : 'development';
        this.selected = {};
        this.render();
    };

    SharedVarsPicker.prototype.toggle = function (name) {
        if (this.selected[name]) delete this.selected[name];
        else this.selected[name] = true;
    };

    SharedVarsPicker.prototype.currentEntries = function () {
        if (this.target !== 'production') return this.devEntries || [];
        // Production values belong to one cluster: show the pool of the host
        // this app actually publishes to, not a merged list.
        const pools = this.production || {};
        const hosts = Object.keys(pools);
        const host = this.host && pools[this.host] ? this.host : hosts[0];
        this.host = host || '';
        return host ? pools[host] : [];
    };

    SharedVarsPicker.prototype.render = function () {
        const container = this.el('Body');
        if (!container) return;
        const id = this.globalName();
        const entries = this.currentEntries();

        let hostNote = '';
        if (this.target === 'production') {
            hostNote = this.host
                ? `<div class="text-xs text-[color:var(--nu-muted)] mb-2">Values for <span class="nu-code">${escapeHtml(this.host)}</span></div>`
                : '<div class="text-xs text-[color:var(--nu-muted)] mb-2">No production values have been resolved yet. Publish the producing application first.</div>';
        }

        if (entries.length === 0) {
            container.innerHTML = hostNote + '<div class="text-xs text-[color:var(--nu-muted)] py-4">Nothing to add.</div>';
            return;
        }

        // App-produced entries first, grouped by producer, then the hand-typed
        // palette — the grouping is what tells the user where a value came from.
        const byApp = {};
        const plain = [];
        entries.forEach((e) => {
            if (e.app) {
                (byApp[e.app] = byApp[e.app] || []).push(e);
            } else {
                plain.push(e);
            }
        });

        function row(e) {
            return `
                <label class="flex items-center gap-2 py-1 text-xs cursor-pointer">
                    <input type="checkbox" onchange="${id}.toggle('${escapeHtml(e.name)}')">
                    <span class="nu-code flex-1">${escapeHtml(e.name)}</span>
                    <span class="text-[color:var(--nu-muted)]">${e.value ? maskValue(e.value) : '—'}</span>
                </label>`;
        }

        let html = hostNote;
        Object.keys(byApp).sort().forEach((app) => {
            html += `<div class="mt-2 text-xs font-semibold">Shared by ${escapeHtml(app)}</div>`;
            html += byApp[app].map(row).join('');
        });
        if (plain.length > 0) {
            html += '<div class="mt-2 text-xs font-semibold">Workspace variables</div>';
            html += plain.map(row).join('');
        }
        container.innerHTML = html;
    };

    SharedVarsPicker.prototype.apply = function () {
        const names = Object.keys(this.selected);
        if (names.length === 0 || !this.table) {
            this.close();
            return;
        }
        const entries = this.currentEntries().filter((e) => this.selected[e.name]);
        this.table.addFromShared(entries, this.target);
        this.close();
    };

    SharedVarsPicker.mount = function (globalName, options) {
        const picker = new SharedVarsPicker(options);
        picker._globalName = globalName;
        global[globalName] = picker;

        const prefix = picker.prefix;
        const container = document.createElement('div');
        container.innerHTML = `
            <div id="${prefix}Modal" class="hidden fixed inset-0 nu-modal-backdrop items-center justify-center z-50 p-4">
              <div class="nu-modal w-full max-w-lg max-h-[85vh] flex flex-col">
                <div class="flex items-center justify-between px-5 py-4 border-b border-[color:var(--nu-border)]">
                  <h3 class="text-lg font-semibold">Add from shared</h3>
                  <button onclick="${globalName}.close()" class="nu-btn nu-btn-secondary nu-btn-compact">Close</button>
                </div>
                <div class="px-5 py-3 border-b border-[color:var(--nu-border)] flex items-center gap-2 text-xs">
                  <span class="text-[color:var(--nu-muted)]">Add to</span>
                  <select onchange="${globalName}.setTarget(this.value)" class="nu-input px-2 py-1 text-xs">
                    <option value="development">Development</option>
                    <option value="production">Production</option>
                  </select>
                </div>
                <div id="${prefix}Body" class="flex-1 overflow-y-auto px-5 py-3"></div>
                <div class="flex gap-2 px-5 py-4 border-t border-[color:var(--nu-border)]">
                  <button onclick="${globalName}.apply()" class="nu-btn nu-btn-primary flex-1">Add selected</button>
                  <button onclick="${globalName}.close()" class="nu-btn nu-btn-secondary flex-1">Cancel</button>
                </div>
              </div>
            </div>`;
        document.body.appendChild(container.firstElementChild);
        return picker;
    };

    global.SharedPicker = SharedPicker;
    global.SharedVarsPicker = SharedVarsPicker;
    global.sharedVarPrefix = varPrefix;
    global.sharedDefaultVarName = defaultVarName;
})(window);

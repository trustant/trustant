// Shared GitHub account connect form.
//
// Trustable pushes and clones over HTTPS with the managed personal GitHub
// account (see spec/github.md); the dedicated SSH key is only the fallback for
// installations that never connect one. The connect form used to exist once, on
// the Configure page, which meant every flow that actually needs repo access —
// Git Push, Add App, My Application Starter — could only offer the user an SSH
// key to paste into GitHub and a suggestion to navigate away. This module is the
// single implementation of that form, mountable into any host element.
//
// Each mount owns its element ids (prefixed per instance) so several copies can
// live on one page, and registers itself globally so the markup's inline
// onclick attributes can reach it — same reason as EnvTable.register in
// envtable.js: a lexical `const` and a window property are two separate slots,
// and inline attributes resolve through the scope chain to the window one.
(function (global) {
    'use strict';

    const DEVICE_URL = 'https://github.com/login/device';
    const POLL_INTERVAL_MS = 1500;
    let instanceCounter = 0;

    function escapeAttribute(text) {
        return String(text).replace(/&/g, '&amp;').replace(/"/g, '&quot;')
            .replace(/</g, '&lt;').replace(/>/g, '&gt;');
    }

    function GitHubAccountForm(container, options) {
        this.container = container;
        this.options = options || {};
        this.globalName = this.options.globalName
            || 'githubAccountForm' + (++instanceCounter);
        this.prefix = this.options.idPrefix || this.globalName;
        this.pollTimer = null;
        this.authenticated = false;
        this.render();
    }

    GitHubAccountForm.prototype.id = function (suffix) {
        return this.prefix + suffix;
    };

    GitHubAccountForm.prototype.element = function (suffix) {
        // `badgeId` lets a host keep the status pill in its own layout (the
        // Configure card header) rather than inside the mounted panel.
        if (suffix === 'Badge' && this.options.badgeId) {
            return document.getElementById(this.options.badgeId);
        }
        return document.getElementById(this.id(suffix));
    };

    GitHubAccountForm.prototype.setVisible = function (suffix, visible) {
        const element = this.element(suffix);
        if (element) element.classList.toggle('hidden', !visible);
    };

    // `compact` drops the status badge, which suits an inline panel inside a
    // modal; the Configure card keeps it.
    GitHubAccountForm.prototype.render = function () {
        const name = escapeAttribute(this.globalName);
        const badge = this.options.compact || this.options.badgeId
            ? ''
            : '<div class="flex justify-end mb-2">'
                + '<span id="' + this.id('Badge') + '" class="nu-status-pill px-2 py-1">Checking...</span>'
                + '</div>';
        this.container.innerHTML = badge
            + '<p id="' + this.id('Message') + '" class="text-sm text-[color:var(--nu-muted)]">'
            + 'Checking managed GitHub status...</p>'
            + '<div id="' + this.id('DevicePanel') + '" class="hidden nu-feedback nu-feedback-info p-3 mt-3">'
            + '<p class="text-sm">Open the device page and enter this one-time code:</p>'
            + '<div class="flex flex-wrap items-center gap-2 mt-2">'
            + '<code id="' + this.id('DeviceCode') + '" class="nu-code text-base font-semibold"></code>'
            + '<button type="button" onclick="' + name + '.copyDeviceCode()"'
            + ' class="nu-btn nu-btn-secondary nu-btn-compact">Copy Code</button>'
            + '<a id="' + this.id('DeviceURL') + '" href="' + DEVICE_URL + '"'
            + ' target="_blank" rel="noopener" class="nu-btn nu-btn-primary">Open GitHub</a>'
            + '</div></div>'
            + '<p id="' + this.id('Error') + '" class="hidden nu-feedback nu-feedback-error p-3 mt-3 text-sm"></p>'
            + '<div class="flex flex-wrap gap-2 mt-4">'
            + '<button id="' + this.id('ConnectBtn') + '" type="button" onclick="' + name + '.startLogin()"'
            + ' class="nu-btn nu-btn-primary">Connect GitHub</button>'
            + '<button id="' + this.id('CancelBtn') + '" type="button" onclick="' + name + '.cancelLogin()"'
            + ' class="hidden nu-btn nu-btn-secondary">Cancel</button>'
            + '<button id="' + this.id('LogoutBtn') + '" type="button" onclick="' + name + '.logout()"'
            + ' class="hidden nu-btn nu-btn-danger">Disconnect</button>'
            + '</div>';
        global[this.globalName] = this;
    };

    GitHubAccountForm.prototype.schedulePoll = function () {
        this.stopPolling();
        const self = this;
        this.pollTimer = setTimeout(function () { self.load(); }, POLL_INTERVAL_MS);
    };

    GitHubAccountForm.prototype.stopPolling = function () {
        if (this.pollTimer) clearTimeout(this.pollTimer);
        this.pollTimer = null;
    };

    GitHubAccountForm.prototype.load = async function () {
        const badge = this.element('Badge');
        const message = this.element('Message');
        const error = this.element('Error');
        try {
            const [statusResponse, loginResponse] = await Promise.all([
                fetch('/api/github/status'),
                fetch('/api/github/login')
            ]);
            const status = await statusResponse.json();
            const login = await loginResponse.json();
            const connecting = !status.authenticated && login.state === 'connecting';
            if (badge) {
                badge.textContent = status.authenticated
                    ? 'Connected'
                    : connecting
                        ? 'Connecting'
                        : status.available
                            ? 'Disconnected'
                            : 'Unavailable';
            }
            message.textContent = status.authenticated
                ? `Connected as ${status.login} on ${status.hostname}.`
                : connecting
                    ? 'Waiting for GitHub device authorization.'
                    : status.error || 'No personal GitHub account is connected.';
            this.setVisible('ConnectBtn', !status.authenticated && !connecting);
            this.setVisible('CancelBtn', connecting);
            this.setVisible('LogoutBtn', status.authenticated);
            const hasDevice = connecting && Boolean(login.url || login.code);
            this.setVisible('DevicePanel', hasDevice);
            if (hasDevice) {
                this.element('DeviceCode').textContent = login.code || 'Waiting...';
                this.element('DeviceURL').href = login.url || DEVICE_URL;
            }
            const loginError = !status.authenticated && login.state === 'failed'
                ? login.error
                : '';
            error.textContent = loginError || '';
            this.setVisible('Error', Boolean(loginError));
            if (connecting) this.schedulePoll();
            // Host pages hang their own behaviour off the status (Configure
            // hides its Git User card; the modals swap their panels), so the
            // callbacks fire after the form itself is consistent.
            if (typeof this.options.onStatus === 'function') this.options.onStatus(status);
            const becameAuthenticated = status.authenticated && !this.authenticated;
            this.authenticated = Boolean(status.authenticated);
            if (becameAuthenticated && typeof this.options.onAuthenticated === 'function') {
                this.options.onAuthenticated(status);
            }
            return status;
        } catch (e) {
            if (badge) badge.textContent = 'Unavailable';
            message.textContent = 'GitHub account status could not be loaded.';
            error.textContent = e.message;
            this.setVisible('Error', true);
            this.setVisible('ConnectBtn', true);
            this.setVisible('CancelBtn', false);
            this.setVisible('LogoutBtn', false);
            this.authenticated = false;
            if (typeof this.options.onStatus === 'function') {
                this.options.onStatus({ authenticated: false, available: false, error: e.message });
            }
            return { authenticated: false, available: false, error: e.message };
        }
    };

    GitHubAccountForm.prototype.startLogin = async function () {
        const error = this.element('Error');
        error.textContent = '';
        this.setVisible('Error', false);
        try {
            const response = await fetch('/api/github/login', { method: 'POST' });
            const data = await response.json();
            if (!response.ok) throw new Error(data.error || 'GitHub login could not start');
            await this.load();
        } catch (e) {
            error.textContent = e.message;
            this.setVisible('Error', true);
        }
    };

    GitHubAccountForm.prototype.cancelLogin = async function () {
        await fetch('/api/github/login/cancel', { method: 'POST' });
        await this.load();
    };

    GitHubAccountForm.prototype.logout = async function () {
        if (!confirm('Disconnect the managed GitHub account from Trustable?')) return;
        const response = await fetch('/api/github/logout', { method: 'POST' });
        const data = await response.json();
        if (!response.ok) {
            const error = this.element('Error');
            error.textContent = data.error || 'GitHub logout failed';
            this.setVisible('Error', true);
        }
        await this.load();
    };

    GitHubAccountForm.prototype.copyDeviceCode = async function () {
        const element = this.element('DeviceCode');
        const code = element.textContent;
        if (!code || code === 'Waiting...') return;
        try {
            await navigator.clipboard.writeText(code);
        } catch (e) {
            const range = document.createRange();
            range.selectNodeContents(element);
            const selection = window.getSelection();
            selection.removeAllRanges();
            selection.addRange(range);
            document.execCommand('copy');
            selection.removeAllRanges();
        }
    };

    // Mounts the form into `container` and starts one status load. Returns the
    // instance so the host can stop polling or re-load on demand.
    function renderGitHubAccountForm(container, options) {
        const element = typeof container === 'string'
            ? document.getElementById(container)
            : container;
        if (!element) return null;
        const form = new GitHubAccountForm(element, options);
        form.load();
        return form;
    }

    global.GitHubAccountForm = GitHubAccountForm;
    global.renderGitHubAccountForm = renderGitHubAccountForm;
})(window);

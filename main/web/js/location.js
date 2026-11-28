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

// location.js tells the server which Trustant page this tab shows, so the MCP
// tool ui_location can answer "where is the user?" (spec/22-mcpserver.md).
// Load it after trustant-auth.js, whose fetch wrapper adds the CSRF header
// when local login is enabled. Best effort: failures are ignored.
(function () {
    'use strict';

    const PAGES = {
        '/applist.html': 'applist',
        '/app.html': 'app',
        '/configure.html': 'configure',
        '/': 'index',
        '/index.html': 'index',
    };

    function cookie(name) {
        const m = document.cookie.match(new RegExp('(?:^|; )' + name + '=([^;]*)'));
        return m ? decodeURIComponent(m[1]) : '';
    }

    function snapshot() {
        const page = PAGES[window.location.pathname] || 'other';
        const body = { page };
        if (page === 'app') {
            body.app = cookie('NAME');
            body.route = cookie('ROUTE') || '/';
            body.query = cookie('QUERY');
        }
        return body;
    }

    function report() {
        if (document.visibilityState === 'hidden') return;
        fetch('/api/v1/location', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(snapshot()),
            keepalive: true,
        }).catch(() => {});
    }

    // Pages call this after changing what they show (e.g. app.html's route).
    window.trustantReportLocation = report;

    report();
    document.addEventListener('visibilitychange', report);
    setInterval(report, 15000);
})();

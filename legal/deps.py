# Copyright 2025-2026 Nuvolaris Inc
#
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU Affero General Public License as published
# by the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# This program is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
# GNU Affero General Public License for more details.
#
# You should have received a copy of the GNU Affero General Public License
# along with this program.  If not, see <https://www.gnu.org/licenses/>.

"""Regenerate DEPS, LICENSE and NOTICE (see spec/20-legal.md).

Run through ./deps.sh from the repo root. Inputs: legal/deps.manual.txt,
go.mod, the npm lockfiles of what we ship, acp/pi.version,
acp/extensions/requirements.txt and oplugins/prereq.yml. Each component's own
LICENSE/NOTICE files are read from node_modules / the Go module cache when
present, otherwise downloaded once into legal/.cache (gitignored). License
texts live in legal/licenses/<SPDX-id>.txt (committed); a missing SPDX text is
fetched from spdx/license-list-data.
"""

import io
import json
import os
import re
import shutil
import subprocess
import sys
import tarfile
import textwrap
import urllib.request
import zipfile

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
LEGAL = os.path.join(ROOT, "legal")
CACHE = os.path.join(LEGAL, ".cache")
LICENSES = os.path.join(LEGAL, "licenses")
SPDX_URL = "https://raw.githubusercontent.com/spdx/license-list-data/main/text/{}.txt"
OWN_LICENSE = "AGPL-3.0-or-later"

# Lockfiles whose production trees are bundled or installed as-is.
NPM_TREES = [
    ("npm-acp", "acp"),
    ("npm-piacp", "acp/pi-acp"),
    ("npm-mcp", "mcp"),
    ("npm-reactmcp", "react-mcp"),
]

SECTIONS = [
    ("go", "GO BINARY (trustant)",
     "Compiled into the single trustant executable."),
    ("web", "FRONTEND ASSETS VENDORED IN web/",
     "Embedded into the trustant executable and served to the browser."),
    ("submodule", "GIT SUBMODULES",
     "Source checked out in this repository and built into the image."),
    ("npm-acp", "TRUACP (acp) - NPM PRODUCTION DEPENDENCIES",
     "Full production tree of acp/package-lock.json, bundled into truacp.cjs\n"
     "and the TruACP web UI."),
    ("npm-piacp", "PI-ACP (acp/pi-acp) - NPM PRODUCTION DEPENDENCIES",
     "Full production tree of acp/pi-acp/package-lock.json."),
    ("npm-mcp", "OPENSERVERLESS-MCP (mcp) - NPM PRODUCTION DEPENDENCIES",
     "Full production tree of mcp/package-lock.json, installed globally in the\n"
     "image."),
    ("npm-reactmcp", "TRUSTANT-REACT-MCP (react-mcp) - NPM PRODUCTION DEPENDENCIES",
     "Full production tree of react-mcp/package-lock.json, installed globally\n"
     "in the image."),
    ("apt", "CONTAINER IMAGE - UBUNTU 24.04 PACKAGES",
     "Installed with apt in image/Dockerfile. Each package's full copyright and\n"
     "license statement ships in the image at /usr/share/doc/<package>/copyright."),
    ("binary", "CONTAINER IMAGE - DOWNLOADED BINARIES AND RUNTIMES",
     "Downloaded by image/Dockerfile (and by start.sh/setup.sh in the dev VM)."),
    ("prereq", "OPS PREREQUISITES (oplugins/prereq.yml)",
     "Binaries `ops` downloads into ~/.ops/<os>-<arch>/bin on first run, in the\n"
     "image and on every developer machine."),
    ("npmglobal", "CONTAINER IMAGE - GLOBAL NPM INSTALLS",
     "Coding agents pinned in acp/pi.version plus the tools installed by\n"
     "image/Dockerfile. Listed at top level; each package's own dependency\n"
     "tree, with its license files, is installed under the global node_modules."),
    ("pytool", "CONTAINER IMAGE - PYTHON TOOLS (uv tool install)",
     "Each tool runs in its own uv-managed virtualenv under /opt/uv/tools."),
    ("python", "PYTHON PACKAGES FOR USER APPS (acp/extensions/requirements.txt)",
     "Installed unmodified for the apps users build."),
    ("dev", "DEVELOPMENT ENVIRONMENT ONLY (NOT DISTRIBUTED)",
     "Installed by start.sh / start.ps1 / setup.sh / run.sh on a developer\n"
     "machine or dev VM. Not part of the binary or the image; no notices are\n"
     "collected for them."),
]

NOTES = """\
LICENSE NOTES

- Claude Code (@anthropic-ai/claude-code) is proprietary. It is NOT included
  in Trustant, its image or this inventory: TruACP installs it on demand into
  the user's workspace only after the user accepts Anthropic's Commercial
  Terms (https://www.anthropic.com/legal/commercial-terms).
- psycopg / psycopg-binary are LGPL-3.0-only; they are installed as unmodified
  Python packages and dynamically imported, as the LGPL permits. Source:
  https://github.com/psycopg/psycopg
- 7-Zip (7zz) is LGPL-2.1-or-later with BSD-3-Clause parts and the unRAR
  restriction; it is the unmodified upstream binary. Source:
  https://www.7-zip.org
- MPL-2.0 components (certifi, ca-certificates, orjson, tqdm) are file-level
  copyleft and are shipped unmodified; sources are at their homepages.
- GPL/LGPL Ubuntu packages are the unmodified distribution binaries; their
  sources are available from the Ubuntu archive (apt-get source <package>).
- air is GPL-3.0 but is a development-only file watcher; it is never
  distributed with Trustant.
"""


def die(msg):
    print("deps.sh: " + msg, file=sys.stderr)
    sys.exit(1)


def warn(msg):
    print("deps.sh: warning: " + msg, file=sys.stderr)


def rel(*p):
    return os.path.join(ROOT, *p)


# ---------------------------------------------------------------- inputs

def read_manual():
    comps = []
    with open(os.path.join(LEGAL, "deps.manual.txt")) as f:
        for n, line in enumerate(f, 1):
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            parts = [p.strip() for p in line.split("|")]
            if len(parts) != 6:
                die("legal/deps.manual.txt:%d: expected 6 fields" % n)
            sec, name, ver, lic, home, fetch = parts
            comps.append(dict(section=sec, name=name, version=ver,
                              license=lic, homepage=home, fetch=fetch))
    return comps


def go_mod():
    goversion, mods = None, []
    inreq = False
    for line in open(rel("go.mod")):
        line = line.split("//")[0].strip()
        if line.startswith("go "):
            goversion = line.split()[1]
        elif line.startswith("require ("):
            inreq = True
        elif inreq and line == ")":
            inreq = False
        elif inreq and line or line.startswith("require "):
            fields = line.replace("require ", "").split()
            if len(fields) >= 2:
                mods.append((fields[0], fields[1]))
    return goversion, mods


def requirements():
    pins = {}
    for line in open(rel("acp/extensions/requirements.txt")):
        m = re.match(r"^([A-Za-z0-9_.\-]+)(?:\[[^]]*\])?==([^\s;#]+)", line)
        if m:
            pins[m.group(1).lower()] = m.group(2)
    return pins


def prereq_versions():
    versions, task = {}, None
    for line in open(rel("oplugins/prereq.yml")):
        m = re.match(r"^  ([a-z0-9_-]+):\s*$", line)
        if m:
            task = m.group(1)
            continue
        m = re.match(r'^\s+VERSION:\s*"?([^"\s]+)"?', line)
        if m and task and not line.lstrip().startswith("#"):
            versions.setdefault(task, m.group(1))
    return versions


def pi_version():
    specs = []
    for line in open(rel("acp/pi.version")):
        line = line.split("#")[0].strip()
        if line:
            specs.append(line)
    return specs


def npm_tree(section, proj):
    lock = json.load(open(rel(proj, "package-lock.json")))
    out = {}
    for key, p in lock.get("packages", {}).items():
        if not key or p.get("dev") or p.get("link") or "node_modules/" not in key:
            continue
        name = key.rsplit("node_modules/", 1)[1]
        ver = p.get("version", "")
        lic = p.get("license") or ""
        local = rel(proj, key)
        out[(name, ver)] = dict(
            section=section, name=name, version=ver, license=lic,
            homepage="https://www.npmjs.com/package/" + name,
            fetch=("dir:" + local) if os.path.isdir(local) else "npm:%s@%s" % (name, ver))
    return list(out.values())


# ---------------------------------------------------------------- fetching

def cache_dir(key):
    return os.path.join(CACHE, re.sub(r"[^A-Za-z0-9_.@=-]+", "_", key))


def http_get(url):
    req = urllib.request.Request(url, headers={"User-Agent": "trustant-deps"})
    with urllib.request.urlopen(req, timeout=60) as r:
        return r.read()


def fetch(comp):
    """Return a directory holding the component's license/notice files, or None."""
    spec = comp["fetch"].replace("{v}", comp["version"])
    if spec == "-" or not spec:
        return None
    kind, _, arg = spec.partition(":")
    if kind == "dir":
        return arg
    if kind == "path":
        return rel(arg)
    if kind == "gomod":
        path, ver = arg.rsplit("@", 1)
        esc = re.sub(r"[A-Z]", lambda m: "!" + m.group(0).lower(), path)
        modcache = subprocess.run(["go", "env", "GOMODCACHE"], capture_output=True,
                                  text=True).stdout.strip()
        d = os.path.join(modcache, "%s@%s" % (esc, ver))
        if not os.path.isdir(d):
            subprocess.run(["go", "mod", "download", "%s@%s" % (path, ver)], cwd=ROOT)
        return d if os.path.isdir(d) else None
    if kind == "pypi" and not arg:
        arg = "%s==%s" % (comp["name"], comp["version"])
    d = cache_dir(kind + "-" + arg)
    if os.path.isdir(d):
        return d
    tmp = d + ".tmp"
    shutil.rmtree(tmp, ignore_errors=True)
    os.makedirs(tmp)
    try:
        if kind == "url":
            for url in arg.split(","):
                data = http_get(url)
                with open(os.path.join(tmp, os.path.basename(url)), "wb") as f:
                    f.write(data)
        elif kind == "npm":
            r = subprocess.run(["npm", "pack", arg, "--silent", "--pack-destination", tmp],
                               capture_output=True, text=True)
            if r.returncode:
                raise RuntimeError(r.stderr.strip()[-300:])
            tgz = [x for x in os.listdir(tmp) if x.endswith(".tgz")][0]
            with tarfile.open(os.path.join(tmp, tgz)) as t:
                extract_top(t.getmembers(), lambda m: t.extractfile(m).read(), tmp, depth=2,
                            keep=lambda n: n.endswith("package/package.json"))
            os.remove(os.path.join(tmp, tgz))
        elif kind == "pypi":
            dl = os.path.join(tmp, "dl")
            base = ["uv", "tool", "run", "--quiet", "--from", "pip", "pip", "download",
                    "--quiet", "--no-deps", "-d", dl, arg]
            r = subprocess.run(base + ["--only-binary=:all:", "--platform",
                                       "manylinux2014_x86_64", "--python-version", "3.12"],
                               capture_output=True, text=True)
            if r.returncode:
                r = subprocess.run(base, capture_output=True, text=True)
            if r.returncode:
                raise RuntimeError(r.stderr.strip()[-300:])
            for fn in os.listdir(dl):
                p = os.path.join(dl, fn)
                if fn.endswith(".whl"):
                    with zipfile.ZipFile(p) as z:
                        names = [n for n in z.namelist() if ".dist-info/" in n]
                        extract_top(names, z.read, tmp, depth=99)
                elif fn.endswith((".tar.gz", ".tgz")):
                    with tarfile.open(p) as t:
                        extract_top(t.getmembers(), lambda m: t.extractfile(m).read(), tmp,
                                    depth=2)
            shutil.rmtree(dl)
        else:
            raise RuntimeError("unknown fetch kind " + kind)
    except Exception as e:  # noqa: BLE001 - report and continue
        warn("%s %s: cannot fetch %s (%s)" % (comp["name"], comp["version"], spec, e))
        shutil.rmtree(tmp, ignore_errors=True)
        return None
    os.rename(tmp, d)
    return d


LEGAL_FILE = re.compile(r"^(LICEN[CS]E|COPYING|NOTICE|UNLICENSE|COPYRIGHT|LICENSE-[A-Z0-9]+)"
                        r"([._-].*)?$", re.I)


def extract_top(members, read, dest, depth, keep=lambda n: False):
    """Copy legal files (and anything `keep` accepts) found at most `depth`
    path components deep from an archive into dest, flattened."""
    for m in members:
        name = m if isinstance(m, str) else m.name
        if not isinstance(m, str) and not m.isfile():
            continue
        parts = name.strip("/").split("/")
        if len(parts) > depth:
            continue
        if LEGAL_FILE.match(parts[-1]) or keep(name):
            out = os.path.join(dest, "__".join(parts[1:]) if len(parts) > 1 else parts[0])
            with open(out, "wb") as f:
                f.write(read(m))


def legal_files(d):
    if not d or not os.path.isdir(d):
        return [], []
    lic, notice = [], []
    for fn in sorted(os.listdir(d)):
        p = os.path.join(d, fn)
        base = fn.split("__")[-1]
        if not os.path.isfile(p) or not LEGAL_FILE.match(base):
            continue
        (notice if base.upper().startswith("NOTICE") else lic).append(p)
    return lic, notice


def read_text(p):
    with open(p, "rb") as f:
        return f.read().decode("utf-8", "replace").replace("\r\n", "\n")


COPYRIGHT = re.compile(r"(?i)^\W*(copyright\b|\(c\)\s|©)")
TEMPLATE = re.compile(r"(?i)\[yyyy\]|<year>|\{yyyy\}|copyright notice|copyright holder|"
                      r"copyright owner|copyright license|copyright and license|"
                      r"copyright law|copyright statement|copyright \(c\) <|"
                      r"copyright,? designs|copyright claims|copyright protection|"
                      r"copyright interest|copyright disclaimer|the copyright|"
                      r"copyright on the|copyright to|copyright in|Free Software Foundation")


def copyright_lines(paths):
    seen, out = set(), []
    for p in paths:
        for line in read_text(p).split("\n"):
            s = re.sub(r"^[\s#*/;!-]+", "", line).strip()
            if not COPYRIGHT.match(s) or TEMPLATE.search(s) or len(s) > 240:
                continue
            if not re.search(r"(19|20)\d\d", s) and not re.match(r"(?i)copyright\s*(\(c\)|©)", s):
                continue
            if s.lower() not in seen:
                seen.add(s.lower())
                out.append(s)
    return out


# ---------------------------------------------------------------- licenses

def license_ids(expr):
    return [t for t in re.findall(r"[A-Za-z0-9.+\-]+", expr)
            if t not in ("AND", "OR", "WITH")]


def normalize_license(lic):
    lic = lic.strip()
    if lic.startswith("(") and lic.endswith(")") and lic.count("(") == 1:
        lic = lic[1:-1]
    return lic


def license_text(lid):
    if lid == OWN_LICENSE:
        return None
    p = os.path.join(LICENSES, lid + ".txt")
    if not os.path.exists(p):
        if lid.startswith("LicenseRef-"):
            die("no text for %s: add legal/licenses/%s.txt" % (lid, lid))
        try:
            data = http_get(SPDX_URL.format(lid))
        except Exception as e:  # noqa: BLE001
            die("cannot fetch SPDX text for %s (%s)" % (lid, e))
        os.makedirs(LICENSES, exist_ok=True)
        with open(p, "wb") as f:
            f.write(data)
    return read_text(p).strip("\n")


# ---------------------------------------------------------------- output

RULE = "=" * 79
THIN = "-" * 79


def table(rows):
    heads = ("NAME", "VERSION", "LICENSE", "HOMEPAGE")
    widths = [max(len(h), *(len(r[i]) for r in rows)) for i, h in enumerate(heads[:3])]
    widths = [min(w, 60) for w in widths]
    lines = []
    for r in [heads, tuple("-" * len(h) for h in heads)] + rows:
        cells = []
        for i in range(3):
            c = r[i]
            cells.append(c + " " * max(2, widths[i] - len(c) + 2))
        lines.append(("".join(cells) + r[3]).rstrip())
    return "\n".join(lines)


def write(name, text):
    with open(rel(name), "w") as f:
        f.write(text.rstrip("\n") + "\n")


def main():
    comps = read_manual()
    goversion, mods = go_mod()
    pins = requirements()
    prereq = prereq_versions()

    for c in comps:
        if c["version"] != "*":
            continue
        if c["section"] == "go":
            c["version"] = goversion
        elif c["section"] == "python":
            c["version"] = pins.pop(c["name"].lower(), None) or die(
                "%s is in deps.manual.txt but not pinned in requirements.txt" % c["name"])
        elif c["section"] == "prereq":
            key = c["name"].split()[0]
            c["version"] = prereq.get(key) or die("no VERSION for %s in prereq.yml" % key)
        elif c["section"] != "npmglobal":  # npmglobal: filled from pi.version
            die("version * not supported in section " + c["section"])
    if pins:
        die("requirements.txt packages missing from legal/deps.manual.txt: "
            + ", ".join(sorted(pins)))

    for path, ver in mods:
        comps.append(dict(section="go", name=path, version=ver.lstrip("v"), license="",
                          homepage="https://" + path, fetch="gomod:%s@%s" % (path, ver)))
    for section, proj in NPM_TREES:
        comps.extend(npm_tree(section, proj))
    for spec in pi_version():
        name, ver = spec.rsplit("@", 1)
        # A manual npmglobal line for a pi.version package only supplies what
        # npm cannot (e.g. the upstream LICENSE its tarball omits).
        manual = [c for c in comps if c["section"] == "npmglobal" and c["name"] == name]
        if manual:
            manual[0]["version"] = ver
            continue
        comps.append(dict(section="npmglobal", name=name, version=ver, license="",
                          homepage="https://www.npmjs.com/package/" + name,
                          fetch="npm:" + spec))

    known = {s[0] for s in SECTIONS}
    for c in comps:
        if c["version"] == "*":
            die("%s: version * but no pinned source provides it" % c["name"])
        if c["section"] not in known:
            die("unknown section %r for %s" % (c["section"], c["name"]))

    # Fetch legal files; fill licenses the inputs do not declare.
    for c in comps:
        d = fetch(c) if c["section"] != "dev" else None
        c["licfiles"], c["noticefiles"] = legal_files(d)
        if not c["license"] and d:
            pj = os.path.join(d, "package.json")
            if os.path.exists(pj):
                c["license"] = json.load(open(pj)).get("license") or ""
        if not c["license"] and c["licfiles"]:
            text = " ".join(read_text(c["licfiles"][0]).split())
            for lid, pat in (("ISC", "Permission to use, copy, modify, and"),
                             ("MIT", "Permission is hereby granted"),
                             ("Apache-2.0", "Apache License"),
                             ("BSD-3-Clause", "Neither the name"),
                             ("BSD-2-Clause", "Redistribution and use")):
                if pat in text:
                    c["license"] = lid
                    break
        c["license"] = normalize_license(c["license"])
        if not c["license"]:
            die("cannot determine the license of %s %s" % (c["name"], c["version"]))

    # DEPS
    out = [RULE, "TRUSTANT - THIRD-PARTY COMPONENTS", RULE, "", textwrap.dedent("""\
        Trustant (https://trustant.ai) is Copyright 2025-2026 Nuvolaris Inc and is
        licensed under the GNU Affero General Public License v3 or later.

        This file lists the third-party software Trustant includes, bundles into
        its container image, downloads at run time or installs into the
        development VM, with each component's license (SPDX expression).

        - LICENSE holds the full text of every license named here.
        - NOTICE holds the copyright and NOTICE statements of the components.

        GENERATED by ./deps.sh from legal/deps.manual.txt, go.mod, the npm
        lockfiles, acp/pi.version, acp/extensions/requirements.txt and
        oplugins/prereq.yml. Do not edit by hand: edit those inputs and rerun.""")]
    n = 0
    for key, title, desc in SECTIONS:
        rows = sorted({(c["name"], c["version"], c["license"], c["homepage"])
                       for c in comps if c["section"] == key}, key=lambda r: (r[0].lower(), r))
        if not rows:
            continue
        n += 1
        head = "%d. %s" % (n, title)
        out += ["", "", head, "=" * len(head), "", desc, "", table(rows)]
    out += ["", "", NOTES]
    write("DEPS", "\n".join(out))

    # LICENSE
    users = {}
    for c in comps:
        for lid in license_ids(c["license"]):
            users.setdefault(lid, set()).add(c["name"])
    own = read_text(os.path.join(LICENSES, OWN_LICENSE + ".txt")).strip("\n")
    out = [own, "", "", RULE, "THIRD-PARTY LICENSES", RULE, "", textwrap.dedent("""\
        The text above is Trustant's own license. Below is the full text of every
        license under which a third-party component listed in DEPS is
        distributed, one per license identifier. DEPS says which license applies
        to which component; NOTICE carries the components' copyright statements.
        """).rstrip("\n"), "", "Licenses in this file:", ""]
    ids = sorted(users, key=str.lower)
    out += ["  " + lid for lid in ids]
    for lid in ids:
        names = sorted(users[lid], key=str.lower)
        used = ("Used by: " + ", ".join(names)) if len(names) <= 25 else \
            "Used by %d components (see DEPS), including: %s, ..." % (
                len(names), ", ".join(names[:12]))
        out += ["", "", RULE, "License: " + lid, RULE,
                textwrap.fill(used, 79, break_on_hyphens=False, break_long_words=False),
                THIN, ""]
        text = license_text(lid)
        out.append(text if text is not None else
                   "This is Trustant's own license; its full text is at the top of "
                   "this file.")
    write("LICENSE", "\n".join(out))

    # NOTICE
    out = [read_text(os.path.join(LEGAL, "NOTICE.head")).strip("\n"), "", "", RULE,
           "THIRD-PARTY NOTICES", RULE, "", textwrap.dedent("""\
        Copyright statements and NOTICE files of the third-party components listed
        in DEPS, grouped as in DEPS. The full license texts are in LICENSE. Ubuntu
        packages carry their statements in /usr/share/doc/<package>/copyright
        inside the container image.""")]
    seen = set()
    n = 0
    for key, title, _ in SECTIONS:
        if key in ("dev",):
            continue
        blocks = []
        for c in sorted((c for c in comps if c["section"] == key),
                        key=lambda c: (c["name"].lower(), c["version"])):
            ident = (c["name"], c["version"])
            if ident in seen:
                continue
            seen.add(ident)
            lines = copyright_lines(c["licfiles"])
            notices = [read_text(p).strip("\n") for p in c["noticefiles"]]
            notices = [t for t in notices if t.strip()]
            if not lines and not notices:
                continue
            b = ["%s %s (%s)" % (c["name"], c["version"], c["license"])]
            b += ["  " + l for l in lines]
            for t in notices:
                b += ["", "  NOTICE:"] + ["  | " + l if l else "  |" for l in t.split("\n")]
            blocks.append("\n".join(b))
        if not blocks:
            continue
        n += 1
        head = "%d. %s" % (n, title)
        out += ["", "", head, "=" * len(head), ""]
        out.append("\n\n".join(blocks))
    write("NOTICE", "\n".join(out))

    missing = [c["name"] for c in comps
               if c["section"] != "dev" and c["fetch"] != "-" and not c["licfiles"]]
    if missing:
        warn("no license file found for: " + ", ".join(sorted(set(missing))))
    print("deps.sh: wrote DEPS (%d components), LICENSE (%d licenses), NOTICE"
          % (len(comps), len(ids)))


if __name__ == "__main__":
    main()

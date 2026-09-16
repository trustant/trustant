# Contributing

Thanks for your interest in contributing to Trustable.

## A Contributor Agreement is required

**We cannot accept a pull request until a signed Contributor License Agreement
(CLA) is on file for its author.** This applies to every contribution of any
size, and to every repository in the Trustable project, including the
`skills`, `oplugins-truinst`, and `trustable-acp` submodules.

Trustable is released under the GNU Affero General Public License, version 3 or
later (see [LICENSE](LICENSE)). Nuvolaris Inc also offers the software under
separate commercial terms. The CLA is what allows us to keep doing both: it
grants Nuvolaris Inc the rights needed to distribute your contribution under
the AGPL *and* under those commercial terms, while you keep the copyright in
the code you wrote.

If you are contributing on behalf of your employer, you need a Corporate CLA
signed by someone authorised to bind that company.

To request an agreement, contact **info@nuvolaris.io** before opening a pull
request. A PR opened without a CLA on file will be left unmerged until one is
signed — please do not spend time on a large change before sorting this out.

## License headers

Every source file carries an AGPL header, enforced by
[license-eye](https://github.com/apache/skywalking-eyes) using the
`.licenserc.yaml` in each repository. Before opening a pull request:

```bash
license-eye header check   # report files missing the header
license-eye header fix     # add it to any file that lacks one
```

Run it from the directory whose files you changed. license-eye discovers files
with `git ls-files`, which does not descend into submodules, so each
Trustable-owned submodule carries its own `.licenserc.yaml` and must be checked
from inside it.

The `oplugins` and `mcp` submodules are upstream Apache-2.0 projects with their
own LICENSE and NOTICE files. Do not add AGPL headers there.

## Before you open a pull request

- `go test ./...` passes.
- Specs under [spec/](spec/) are updated when behaviour changes — the spec is
  the source of truth, and is iterated on before the code.
- The change is scoped to one issue.

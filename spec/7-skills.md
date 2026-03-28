This file describes the skills feature.
Put the backend code in `skills.go`.

# Initial skill setup

On every app launch:
- if `.agents` does not exist, create it
- if `.agents` exists and is not empty, skip (skills already installed)
- if `.agents` exists and is empty, proceed to add skills

# Adding the skills

1. Determine the skills repo: read `OPS_SKILLS` from `.env.production` if available and not empty, otherwise from `.env`, otherwise fall back to the global `OpsSkills` default (`trustable-ai/skills`)
2. `cd` into the `.agents` directory
3. `git clone <repo> skills` — try SSH first (`git@github.com:<repo>.git` with `GIT_SSH_COMMAND` using the ed25519 key and `StrictHostKeyChecking=no`). If SSH fails, clean up and fall back to HTTPS (`https://github.com/<repo>.git`). This is the same pattern used by repo cloning.
4. Remove `skills/.git`
5. `git add .agents/skills`
6. `git commit -m "added skills"`
7. `git push origin`
8. The launch API returns `"skills_added": true` and the UI shows a popup informing "Added skills"

# API

## GET /api/skills/\<name\>

Returns the list of skills and the source repo.

1. Validate `name` with the standard name pattern
2. Check workbench exists
3. Read each subfolder under `.agents/skills/`, parse `SKILL.md` (front matter + markdown body)
4. Return `{"skills": [...], "source": "<repo>"}` where each skill has `name`, `front_matter` (key-value map), and `body` (markdown string)

## POST /api/skills/\<name\>/update

Removes existing skills and re-clones.

1. Remove `.agents/skills`
2. Re-run the "adding the skills" process
3. Return `{"status": "updated"}`

# Skills UI

In `app.html` toolbar, show a "Skills" button. On click, show a modal with:

- **Source line**: link to the GitHub repo and an "Update Skills to Latest" button
- **Message**: "Configure OPS_SKILLS in main window to use your own skill repo."
- **Tabbed interface**: one tab per folder under `.agents/skills/`
- Each tab shows:
  - Front matter key-value pairs in a table
  - Body as rendered markdown in a scrollable div

Clicking "Update Skills to Latest" shows a confirmation: "Warning: I will remove current skills and replace with the latest." On accept, calls POST `/api/skills/<name>/update` and reloads the skills list.

# Template workflow end-to-end scenario

1. Start Trustable through the normal FQDN route and launch an application.
2. Open TruACP, connect an agent, and open **Templates**.
3. Confirm `trustable-ai/templates` and branch `main` load without
   `NOTEBOOK_GITHUB_TOKEN`.
4. Select a template with three `---`-separated prompts. Confirm `template.md`
   appears at the workbench root with front matter naming the template, its
   repository and file, and `edited: false`, and that the first radio is
   selected.
5. Confirm every node reads **Not run** with a muted left edge. Run the first
   node from its **Run** control, then run the second with **Run next step**.
   Confirm each response is directly below its prompt, selection advances once,
   the node in flight shows **Running…** with an amber edge, and finished nodes
   turn green and read **Run** while later ones stay **Not run**.
6. Enter an ad-hoc prompt. Confirm it is inserted before the selected node,
   runs normally, and does not advance selection.
7. Pin the ad-hoc node. Press **Edit** on another node and confirm the prompt
   becomes editable in place, the composer is untouched, and **Save**/**Cancel**
   replace Run/Remove. Cancel and confirm the prompt is unchanged.
8. Edit again, change the text, and **Save**. Confirm the node shows the new
   prompt, the node did **not** run, and `template.md` on disk now contains the
   edit with `edited: true`.
9. Reopen the panel. Confirm the working copy is highlighted with a **Changed**
   badge and shows editable name and file fields.
10. Reload the browser and reopen the panel. Confirm the Changed state survives,
    since it is recorded in the file rather than in session state.
11. Select the first node and press **Run all steps**. Confirm every remaining
    node runs in order, one at a time, that unpinned ad-hoc nodes are skipped,
    and that selection clears at the end.
12. Without `NOTEBOOK_GITHUB_TOKEN`, confirm the panel shows no read-only
    warning, no add-template section, and no **Save to GitHub** button — only
    the unhighlighted note *"add in configuration your github token to edit
    templates"*, and no token field. Confirm the Changed badge and the name and
    file fields are still shown.
13. Try to select a different template. Confirm it asks before replacing the
    edited working copy, and that cancelling leaves `template.md` untouched.
14. Configure a repository-scoped token in Trustable **Configure → Template
    Repository**. Reopen the panel, change the name, and press **Save to
    GitHub**. Confirm the file and its `README.md` entry land in the configured
    repository under the new name, and that `template.md` now reads
    `edited: false`.
15. Select a different template. Confirm it is replaced without a prompt, now
    that the working copy has no unsaved changes.
16. Add and remove a fixture template. Confirm `README.md` and the template
    file change together, or that any partial GitHub mutation is reported, and
    that removing a catalog entry leaves the working copy in place.
17. Start a new session with no template loaded. Send an ordinary chat message,
    press **Pin** on it, and confirm a `template.md` with a blank `name:` is
    created, the message becomes the first step, and its reply is carried over.
18. Run `git status` in the workbench. Confirm `template.md` is staged but not
    committed, then save the application in Trustable and confirm it is
    committed and pushed with the user's other changes.
19. Resume and fork the ACP session; confirm template nodes, outputs,
    selection, and provenance restore. Start an ordinary new chat without
    loading a template and confirm its behavior is unchanged.

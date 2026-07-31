# Template workflow end-to-end scenario

1. Start Trustable through the normal FQDN route and launch an application.
2. Open TruACP, connect an agent, and open **Templates**.
3. Confirm `trustable-ai/templates` and branch `main` load without
   `NOTEBOOK_GITHUB_TOKEN`.
4. Load a fixture containing three `---`-separated prompts and confirm the
   first radio is selected.
5. Run the first node from its **Run** control, then run the second with
   **Run next step**. Confirm each response is directly below its prompt and
   selection advances once.
6. Enter an ad-hoc prompt. Confirm it is inserted before the selected node,
   runs normally, and does not advance selection.
7. Pin the ad-hoc node. Press **Edit** on another node and confirm the prompt
   becomes editable in place, the composer is untouched, and **Save**/**Cancel**
   replace Run/Remove. Cancel and confirm the prompt is unchanged.
8. Edit again, change the text, and **Save**. Confirm the node shows the new
   prompt, the template is dirty, and the node did **not** run.
9. Select the first node and press **Run all steps**. Confirm every remaining
   node runs in order, one at a time, that unpinned ad-hoc nodes are skipped,
   and that selection clears at the end.
10. Run the final node alone. Confirm selection clears and **Run next step** is
    disabled.
11. Without `NOTEBOOK_GITHUB_TOKEN`, confirm the panel shows no read-only
    warning, no add-template section, and no loaded-template name row — only the
    unhighlighted note *"add in configuration your github token to edit
    templates"*, and no token field.
12. Still without a token, edit a node and save. Confirm `template.md` appears
    at the workbench root of the launched application, contains the prompts in
    deterministic `---` format, and is staged in git.
13. Reopen the panel and confirm **Saved Template** is listed first, ahead of
    the indexed templates, and loads the local prompts.
14. Configure a repository-scoped token in Trustable **Configure → Template
    Repository**, reload the source, save, and confirm the GitHub file contains
    only notebook/pinned prompts in deterministic `---` format.
15. Add and remove a fixture template. Confirm `README.md` and the template
    file change together, or that any partial GitHub mutation is reported.
16. Modify the remote template SHA independently and confirm a stale save is
    rejected without overwriting it.
17. Resume and fork the ACP session; confirm template nodes, outputs,
    selection, dirty state, and the Saved-Template origin restore. Start an
    ordinary new chat without loading a template and confirm its behavior is
    unchanged.

# Notebook workflow end-to-end scenario

1. Start Trustable through the normal FQDN route and launch an application.
2. Open TruACP, connect an agent, and open **Notebook**.
3. Confirm `trustable-ai/notebooks` and branch `main` load without
   `NOTEBOOK_GITHUB_TOKEN`.
4. Load a fixture containing three `---`-separated prompts and confirm the
   first radio is selected.
5. Run the first node from its **Run** control, then run the second with
   **Run next**. Confirm each response is directly below its prompt and
   selection advances once.
6. Enter an ad-hoc prompt. Confirm it is inserted before the selected notebook
   node, runs normally, and does not advance selection.
7. Pin the ad-hoc node, edit another node through **Edit**, submit it, and
   confirm edit-and-run advances selection.
8. Run the final node. Confirm selection clears and **Run next** is disabled.
9. Without `NOTEBOOK_GITHUB_TOKEN`, confirm save/add/remove are disabled and
   the UI asks for the server `.env` variable without presenting a token field.
10. Restart with a repository-scoped token in the server `.env`, reload the
    source, save, and confirm the GitHub file contains only notebook/pinned
    prompts in deterministic `---` format.
11. Add and remove a fixture notebook. Confirm `README.md` and the notebook
    file change together, or that any partial GitHub mutation is reported.
12. Modify the remote notebook SHA independently and confirm a stale save is
    rejected without overwriting it.
13. Resume and fork the ACP session; confirm notebook nodes, outputs,
    selection, and dirty state restore. Start an ordinary new chat without
    loading a notebook and confirm its behavior is unchanged.

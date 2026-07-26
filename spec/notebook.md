
Add notebook support to TruACP.

# Intro

This is the current UI for TruACP:

![[SCR-20260725-smtv.png]]

It shows a chat interface with a sequence of input/output turns.

# Notebooks

A **notebook** is a set of predefined prompts that can be loaded into the chat and executed sequentially.

## Source

Notebooks are stored in a GitHub repository. The default source is `trustable-ai/notebooks` (https://github.com/trustable-ai/notebooks).

## Format

A notebook is a plain markdown file. Prompts are separated by `---`:

```
First prompt text

---

Second prompt text

---

Third prompt text
```

The file `README.md` in the repo acts as the index. It is parsed for entries of the form:

```
- [name](notebook-file.md) optional comment
```

Each entry corresponds to one notebook.

# Toolbar

The toolbar gains two new controls:

| Control | Description |
| --- | --- |
| **Notebook** button | Opens the notebook panel to browse and load notebooks |
| **Run next** button | Executes the currently selected notebook node and advances selection to the next |

# Notebook Panel

Clicking **Notebook** opens a side panel with:

- A **source field** showing the current GitHub repository (default: `trustable-ai/notebooks`)
- A **pen icon** to change the repository URL and optionally enter a GitHub token (required to save back)
- The list of notebooks parsed from `README.md` — clicking a name loads that notebook into the chat
- A **save button** (disabled unless a GitHub token is set) to write the current notebook back to the repo
- Controls to **add**, **remove**, or **rename** notebooks — changes are reflected in `README.md` and the repository

# Notebook Nodes in Chat

When a notebook is loaded, each prompt appears in the conversation as a **notebook node**. Notebook nodes are visually distinct from regular chat turns and show:

| Control | Action |
| --- | --- |
| Radio button | Selects this node as the current node |
| Arrow icon | Executes this node |
| Pen icon | Edits this node |
| Trash icon | Removes this node |

Selection starts at the first node when the notebook is loaded.

## Executing a Notebook Node

Clicking the arrow icon (or **Run next** on the toolbar):

1. Sends the prompt to the LLM
2. Appends the LLM output immediately after the node in the chat
3. Advances selection to the next notebook node

## Editing a Notebook Node

Clicking the pen icon:

1. Copies the prompt text into the chat textarea
2. The user edits the text and submits
3. On submit: the node is updated with the new text **and** executed immediately (output appended, selection advances)

# Ad-hoc Input Nodes

If the user types in the textarea **without** clicking a pen icon first, the text is treated as an ad-hoc input:

1. An **input node** is inserted before the currently selected notebook node
2. The input is sent to the LLM and output is appended after it
3. Selection remains on the next notebook node

Input nodes are not part of the notebook by default. Each input node shows a **pin icon**. Clicking the pin promotes the input node to a full notebook node (it then gains the standard radio / arrow / pen / trash controls and is included in saves).

# Saving

Clicking **Save** in the notebook panel writes the current notebook back to GitHub:

- Only notebook nodes are saved (pinned nodes that have been promoted are included; unpinned input nodes are not)
- The file is saved in the same `---`-separated markdown format the notebook was loaded from
- Changes to the notebook list (add / remove / rename) are also saved to `README.md`
- The save button is disabled unless a GitHub token has been provided

# TODO

## Features:

- [x] `:copy` `/copy` to copy the latest message to the clipboard
- [ ] `:new` `/new` to create a new session
- [ ] `:model` and `/model` command to switch models of the current provider
- [ ] `:compact`` `/compact` to compact the session (remove the bloat - let the agent say what it wants to keep and discard the rest)
- [ ] Add a permission system to allow agent to ask for permission some commands (e.g. rm)
- [ ] Add a chat mode, where the agent doesn't have any tools
- [ ] Add a :reasoning command to control the reasoning level
- [ ] Add a :debug command to toggle debug mode
- [ ] Add a :load command to load a session from a file
  [ ] - Display the previous session ui / title in fzf to allow the user to select the session to load
- [ ] Add a title system (small llm call to generate a title for the session)
- [ ] bind ctrol u / d to scroll up / down 1/2 a page
- [ ] TUI: escape to cancel the last msg
- [ ] TUI: a `!` to run a command and be able to happen in to the next message (the truncated output)
- [x] keyboard shortcuts with ctrl+x y (for to copy the latest message to the clipboard)
- [ ] Bind the scroll up / down keys to scroll the page up / down



Some tools ideas:
- `todo list` tool for the user to be able to add tasks / cross them off
- LSP search
- Go module desciption (the `go help` and the all the Public function signature + description)
- indexing the codebase for search (TBD on how)

---

## Improvements:
- since we are pretty much already waiting on the inference, the `working...` message is always displayed.
  - Let's have a `isInferenceRunning`, which when true, make the spinner spin
  - And let's keep the current status for the other stuff, like which tool is being used
  - We could use this status for the `thinking: what is the model doing` as well
  - Maybe for some tools, we could display them in the UI, like a full line to say: reading the file `file.txt`
  - executing bash commands (with cropping the output)

---

## Bugs:
- the page up can go beyond the first message

--- 

## Research:

#### SubAgent
- Explore subagent
  1. The parent model decides a request needs broad codebase exploration
  2. It calls task with `subagent_type: "explore"` and a detailed prompt
  3. Creates a persisted child session linked to the parent session
  4. The Explore agent searches using tools such as Glob, Grep, Read, and Bash
  5. The task waits synchronously for the child session to finish
  6. The child’s final text response is returned to the parent
  7. The parent summarizes that result for the user

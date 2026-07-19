# TODO

- `:copy` `/copy` to copy the latest message to the clipboard
- keyboard shortcuts with ctrl+x y (for to copy the latest message to the clipboard)
- Add a :model command to switch models
- Add a permission system to allow agent to ask for permission some commands (e.g. rm)
- Add a chat mode, where the agent doesn't have any tools
- Add a :reasoning command to control the reasoning level
- Add a :debug command to toggle debug mode
- Add a :load command to load a session from a file
  - Display the previous session ui / title in fzf to allow the user to select the session to load
- Add a title system (small llm call to generate a title for the session)

Some tools ideas:
- LSP search
- Go module desciption (the `go help` and the all the Public function signature + description)
- indexing the codebase for search (TBD on how)

-- 

## SubAgent
- Explore subagent
  1. The parent model decides a request needs broad codebase exploration
  2. It calls task with `subagent_type: "explore"` and a detailed prompt
  3. Creates a persisted child session linked to the parent session
  4. The Explore agent searches using tools such as Glob, Grep, Read, and Bash
  5. The task waits synchronously for the child session to finish
  6. The child’s final text response is returned to the parent
  7. The parent summarizes that result for the user

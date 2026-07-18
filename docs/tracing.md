# Tracing

Definition:
> Detailed record of everything that happened during a single execution of the system


What's will need:
- input
  - user prompt
  - config
  - env variable
  - repository / commit / state
- Execution
  - LLM Call (input/output)
  - Tool invocation
  - Function call
  - Agent reasoning steps
- Artefacts
  - Files created or modified
  - Generated patches
  - Logs
  - Test results

We want to record the timestamp, duration, cost if available, token amount. 

As well as the outcome:
- Success/Failure
- Error
- User behavior
- Final anwser

So we can that this and do an eval

---

## The goal:
- Have the ability to debug / understand what happened
- Have the ability to load a state
- Evaluate / comparate trace
- Audit
- Observability

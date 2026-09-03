# MCP interoperability witness — `sensei-code control`

**What this is.** The remote control surface driven by the **official MCP
TypeScript SDK client** (`@modelcontextprotocol/sdk@1.30.0`) over Streamable
HTTP. It exists because our own hand-written client talking to our own
hand-written server proves only that the two agree with each other. This
establishes that the endpoint is a real MCP server rather than JSON-RPC that
resembles one.

It is **not** the ChatGPT / Secure-MCP-Tunnel proof. That belongs to a later
slice, over a tunnel, with a real remote agent.

## Method

```text
binary      sensei-code control --addr 127.0.0.1:18120   (the shipping command)
workspace   resolved live from Sensei, not configured
client      @modelcontextprotocol/sdk 1.30.0, Client + StreamableHTTPClientTransport
transport   Streamable HTTP, Authorization: Bearer <token>
node        v24.4.0
```

`client.connect(transport)` performs `initialize` and sends
`notifications/initialized`; the SDK adds `MCP-Protocol-Version` to every later
request itself. The task inspected is a real record from this repository, not a
fixture.

The credential was supplied through `SENSEI_CODE_CONTROL_TOKEN`. The server's
banner confirms it was **not** printed back:

```text
Sensei Code control surface
  workspace    github.com/globulario/sensei-code
  endpoint     http://127.0.0.1:18120/mcp
  protocol     MCP 2025-06-18
  principal    remote:ed61efdf6788b178
  credential   supplied through SENSEI_CODE_CONTROL_TOKEN and not repeated here
```

A second run without that variable was checked separately: the minted token is
written to `.sensei-code/control-token` at mode `0600` (64 hex characters),
never appears on stdout, and is removed on clean shutdown. `.sensei-code/` is
gitignored, so it cannot be committed.

## What the boundary did, rather than what it claims

- `implementer` was requested alongside architect and reviewer, and **refused**
  with its reason.
- The architect session was granted `inspect_task, submit_architecture`; the
  reviewer session `inspect_task, submit_review`. Neither holds the other's.
- `submit_review` as a **tool** does not exist: the official client's call
  failed with `MCP error -32602: this control surface has no tool
  "submit_review"`. A capability named in a lease is not a verb on this surface.
- `graph_generation` came back `unavailable`, carrying the generation the record
  was written at — this read-only surface does not ask the live graph whether
  those facts are still current, so it does not answer.
- `open_findings` came back `empty-proven`, distinct from the `absent` an
  unknown task returns.

## Transcript

```text

=== initialize (server response, as the SDK recorded it) ===
{
  "serverInfo": {
    "name": "sensei-code-control",
    "version": "1"
  },
  "capabilities": {
    "tools": {}
  },
  "instructions": "Authentication reaches this surface and grants no role. Call register_role to request architect or reviewer, and present the returned role session on every later call. This slice is read-only: no architecture or review may be submitted, and nothing here advances a task."
}

=== tools/list ===
[
  "register_role",
  "release_role",
  "renew_role",
  "get_work",
  "inspect_task"
]

=== tools/call register_role ===
{
  "granted": [
    {
      "authority": "architectural",
      "capabilities": [
        "inspect_task",
        "submit_architecture"
      ],
      "expires_at": "2026-09-04T00:10:59.004847473Z",
      "granted_at": "2026-09-03T23:55:43.60139515Z",
      "renewed_at": "2026-09-03T23:55:59.004847473Z",
      "role": "architect",
      "role_session": "rs-7ca7d3100b6af4823545b6a4c725e034",
      "state": "active"
    },
    {
      "authority": "execution",
      "capabilities": [
        "inspect_task",
        "submit_review"
      ],
      "expires_at": "2026-09-04T00:10:59.004847473Z",
      "granted_at": "2026-09-03T23:55:43.60139515Z",
      "renewed_at": "2026-09-03T23:55:59.004847473Z",
      "role": "reviewer",
      "role_session": "rs-1a56c92ea07ecffe5de7867aac7cd359",
      "state": "active"
    }
  ],
  "label": "Witness",
  "notice": "This role session grants what its capabilities list says and nothing else. This slice is read-only: no architecture or review can be submitted, nothing here advances a task, and no review conducted through this surface can be independent.",
  "principal": "remote:ed61efdf6788b178",
  "refused": [
    {
      "reason": "the implementer role is never granted to a remote principal: it mutates a candidate, and candidate mutation stays owned by Sensei Code",
      "role": "implementer"
    }
  ],
  "workspace": "github.com/globulario/sensei-code"
}

=== tools/call get_work (first 3) ===
{
  "workspace": "github.com/globulario/sensei-code",
  "role": "architect",
  "work": [
    {
      "phase": "implementing",
      "record": {
        "state": "present",
        "source": ".sensei-code/tasks/task-1786919642329466564.json"
      },
      "task": "task-1786919642329466564",
      "task_text": "Add a table-driven unit test proving that an unknown TUI slash command cannot start governed execution. Do not change product behaviour."
    },
    {
      "phase": "implementing",
      "record": {
        "state": "present",
        "source": ".sensei-code/tasks/task-1786921348491025874.json"
      },
      "task": "task-1786921348491025874",
      "task_text": "Add a table-driven unit test proving that an unknown TUI slash command cannot start governed execution. Do not change product behaviour."
    },
    {
      "phase": "revising",
      "record": {
        "state": "present",
        "source": ".sensei-code/tasks/task-1786925496379817744.json"
      },
      "task": "task-1786925496379817744",
      "task_text": "Add a table-driven unit test proving that an unknown TUI slash command cannot start governed execution. Do not change product behaviour."
    }
  ],
  "total_tasks": 23
}

=== tools/call inspect_task ===
{
  "authority": {
    "state": "present",
    "source": ".sensei-code/tasks/task-1786919642329466564.json",
    "structured": {
      "decisions": [
        {
          "chosen": "Authorize the architectural change described above",
          "condition": "graph coverage is absent for the planned files",
          "decided_at": "2026-08-16T22:36:50.365678767Z",
          "durable": true,
          "question": "Architectural authority reached a human-owned boundary."
        }
      ]
    }
  },
  "base": {
    "state": "present",
    "source": ".sensei-code/tasks/task-1786919642329466564.json",
    "text": "0b065920bffd35b5bd164b44abf86c45ef7bc58f",
    "structured": {
      "base_sha": "0b065920bffd35b5bd164b44abf86c45ef7bc58f"
    }
  },
  "candidate": {
    "state": "present",
    "source": ".sensei-code/tasks/task-1786919642329466564.json",
    "structured": {
      "branch": "sensei-code/task-1786919642329466564",
      "changed_paths": null,
      "worktree": "/home/dave/Documents/github.com/globulario/.sensei-code-worktrees/task-1786919642329466564"
    }
  },
  "contract": {
    "state": "present",
    "source": ".sensei-code/tasks/task-1786919642329466564.json",
    "structured": {
      "consequences": "The repository gains behavioral proof at the TUI dispatch boundary that unknown slash commands remain rejected and cannot start governed work. Runtime behavior and production code remain unchanged.",
      "files": null,
      "invariants": null,
      "plan": "",
      "rationale": "Add TUI-boundary regression coverage ensuring unknown slash commands cannot enter governed execution, with production behavior unchanged.",
      "steps": [
        "Add table-driven TestUnknownSlashCommandCannotStartGovernedExecution covering typos, nonexistent commands, and command-prefix collisions.",
        "Drive each case through Model.Update with an intentionally unusable workflow engine and assert busy, currentTask, and running remain unset while unknown-command feedback appears.",
        "Run gofmt -w cmd internal, go vet ./..., and go test ./...."
      ]
    }
  },
  "evidence": {
    "state": "present",
    "source": ".sensei-code/tasks/task-1786919642329466564.json",
    "structured": {
      "audit_detail": "",
      "audit_verdict": "",
      "diff_bytes": 0,
      "report_bytes": 0,
      "required_tests": [
        "Coverage is thin — read the surrounding code, then re-run preflight with --file to narrow"
      ]
    }
  },
  "graph_generation": {
    "state": "unavailable",
    "source": ".sensei-code/tasks/task-1786919642329466564.json",
    "structured": {
      "observed_at": "2026-08-16T22:39:34.292369658Z",
      "recorded_graph_build_commit": "9723c9b177f1"
    },
    "reason": "this is the generation the record was written at; whether it is still current was not checked by this read-only surface"
  },
  "open_findings": {
    "state": "empty-proven",
    "source": ".sensei-code/tasks/task-1786919642329466564.json",
    "reason": "the record holds no unanswered findings for this task"
  },
  "record": {
    "state": "present",
    "source": ".sensei-code/tasks/task-1786919642329466564.json",
    "structured": {
      "domain": "github.com/globulario/sensei-code",
      "session": "session-20260816T223402.328564606Z",
      "task_text": "Add a table-driven unit test proving that an unknown TUI slash command cannot start governed execution. Do not change product behaviour.",
      "updated_at": "2026-08-16T22:39:34.29237041Z",
      "version": 2
    }
  },
  "task": "task-1786919642329466564",
  "workers": {
    "state": "present",
    "source": ".sensei-code/tasks/task-1786919642329466564.json",
    "structured": {
      "workers": [
        "claude"
      ]
    }
  },
  "workflow_state": {
    "state": "present",
    "source": ".sensei-code/tasks/task-1786919642329466564.json",
    "text": "implementing",
    "structured": {
      "phase": "implementing"
    }
  },
  "workspace": "github.com/globulario/sensei-code"
}

=== boundary: capabilities actually granted ===
{
  "architect": [
    "inspect_task",
    "submit_architecture"
  ],
  "reviewer": [
    "inspect_task",
    "submit_review"
  ],
  "refused": [
    {
      "reason": "the implementer role is never granted to a remote principal: it mutates a candidate, and candidate mutation stays owned by Sensei Code",
      "role": "implementer"
    }
  ]
}

=== boundary: submit_review ===
{
  "refused": "MCP error -32602: this control surface has no tool \"submit_review\""
}

=== witness complete ===
```
